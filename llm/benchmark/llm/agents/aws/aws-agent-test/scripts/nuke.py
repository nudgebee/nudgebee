#!/usr/bin/env python3
"""Nuke all aws-agent-test CloudFormation stacks using boto3.

Usage:
    python nuke.py              # interactive confirmation
    python nuke.py --yes        # skip confirmation (for CI/automation)
"""

import argparse
import os
import sys
import time

import boto3
from botocore.exceptions import ClientError

REGION = os.environ.get("AWS_REGION", "us-east-1")
STACK_PREFIX = "aws-agent-test"
MANAGED_BY_TAG = "nudgebee-benchmark"
WAIT_TIMEOUT = int(os.environ.get("NUKE_WAIT_TIMEOUT", "300"))
POLL_INTERVAL = 10
S3_DELETE_BATCH_SIZE = 1000  # AWS API limit


def _has_managed_tag(cf_client, stack_name):
    """Check if a stack has the managed-by=nudgebee-benchmark tag."""
    try:
        resp = cf_client.describe_stacks(StackName=stack_name)
        tags = resp["Stacks"][0].get("Tags", [])
        return any(
            t.get("Key") == "managed-by" and t.get("Value") == MANAGED_BY_TAG
            for t in tags
        )
    except ClientError:
        return False


def get_stacks(cf_client):
    """Find all aws-agent-test stacks tagged by the benchmark system."""
    active_statuses = [
        "CREATE_COMPLETE", "UPDATE_COMPLETE", "ROLLBACK_COMPLETE",
        "UPDATE_ROLLBACK_COMPLETE", "CREATE_FAILED", "DELETE_FAILED",
    ]
    paginator = cf_client.get_paginator("list_stacks")
    candidates = []
    for page in paginator.paginate(StackStatusFilter=active_statuses):
        for s in page.get("StackSummaries", []):
            if s["StackName"].startswith(f"{STACK_PREFIX}-"):
                candidates.append(s["StackName"])

    # Prefer tagged stacks; fall back to all prefix-matched if none are tagged
    tagged = [s for s in candidates if _has_managed_tag(cf_client, s)]
    if tagged:
        skipped = len(candidates) - len(tagged)
        if skipped:
            print(f"    Skipped {skipped} stack(s) without managed-by={MANAGED_BY_TAG} tag")
        return sorted(tagged)

    # No tagged stacks — fall back to prefix match (handles old/untagged resources)
    if candidates:
        print(f"    No tagged stacks found, falling back to prefix match ({len(candidates)} stack(s))")
    return sorted(candidates)


def wait_for_delete(cf_client, stack_name):
    """Wait for a stack to be deleted, with timeout and early exit if already gone."""
    elapsed = 0
    while elapsed < WAIT_TIMEOUT:
        try:
            resp = cf_client.describe_stacks(StackName=stack_name)
            stacks = resp.get("Stacks", [])
            if not stacks:
                print(f"    {stack_name} deleted.")
                return True
            status = stacks[0]["StackStatus"]
        except ClientError as e:
            if e.response.get("Error", {}).get("Code") == "ValidationError":
                print(f"    {stack_name} deleted.")
                return True
            raise

        if status == "DELETE_COMPLETE":
            print(f"    {stack_name} deleted.")
            return True
        elif status == "DELETE_FAILED":
            reason = stacks[0].get("StackStatusReason", "unknown reason")
            print(f"    WARNING: {stack_name} DELETE_FAILED: {reason}")
            return False
        elif status == "DELETE_IN_PROGRESS":
            print(f"    {stack_name}: {status} ({elapsed}s/{WAIT_TIMEOUT}s)")
        else:
            # Stack rolled back to a non-delete state (e.g. CREATE_COMPLETE)
            print(f"    WARNING: {stack_name} delete rolled back to {status}")
            return False

        time.sleep(POLL_INTERVAL)
        elapsed += POLL_INTERVAL

    print(f"    WARNING: {stack_name} timed out after {WAIT_TIMEOUT}s")
    return False


def empty_bucket(s3_client, bucket_name):
    """Empty an S3 bucket before deletion, batching deletes to API limit."""
    total_deleted = 0
    try:
        # Remove bucket policy first (might block object deletion)
        try:
            s3_client.delete_bucket_policy(Bucket=bucket_name)
            print(f"    Removed bucket policy for {bucket_name}")
        except ClientError:
            pass  # No policy or already removed

        paginator = s3_client.get_paginator("list_object_versions")
        for page in paginator.paginate(Bucket=bucket_name):
            objects = []
            for v in page.get("Versions", []):
                objects.append({"Key": v["Key"], "VersionId": v["VersionId"]})
            for m in page.get("DeleteMarkers", []):
                objects.append({"Key": m["Key"], "VersionId": m["VersionId"]})
            # Batch deletes to S3 API limit
            for i in range(0, len(objects), S3_DELETE_BATCH_SIZE):
                batch = objects[i:i + S3_DELETE_BATCH_SIZE]
                if batch:
                    s3_client.delete_objects(Bucket=bucket_name, Delete={"Objects": batch})
                    total_deleted += len(batch)
        print(f"    Emptied bucket {bucket_name}: {total_deleted} object(s) deleted")
    except Exception as e:
        print(f"    Warning: failed to empty bucket {bucket_name} ({total_deleted} deleted so far): {e}")


def get_stack_s3_resources(cf_client, stack_name):
    """Get logical and physical IDs of S3 buckets from a stack."""
    try:
        paginator = cf_client.get_paginator("list_stack_resources")
        buckets = []
        for page in paginator.paginate(StackName=stack_name):
            for r in page.get("StackResourceSummaries", []):
                if r["ResourceType"] == "AWS::S3::Bucket":
                    buckets.append({
                        "logical_id": r["LogicalResourceId"],
                        "physical_id": r.get("PhysicalResourceId", ""),
                    })
        return buckets
    except ClientError:
        return []


def delete_bucket(s3_client, bucket_name):
    """Force-delete an S3 bucket (empty it first if needed)."""
    try:
        s3_client.delete_bucket(Bucket=bucket_name)
        print(f"    Deleted bucket {bucket_name}")
    except ClientError as e:
        print(f"    Warning: could not delete bucket {bucket_name}: {e}")


def delete_stack_with_retry(cf_client, s3_client, stack_name):
    """Delete a stack, retrying with RetainResources for S3 buckets if needed."""
    # First attempt: normal delete
    print(f"==> Deleting {stack_name}...")
    cf_client.delete_stack(StackName=stack_name)
    if wait_for_delete(cf_client, stack_name):
        return True

    # Delete failed — likely due to S3 bucket. Get bucket info BEFORE retry
    # (stack must still exist at this point for describe to work).
    s3_buckets = get_stack_s3_resources(cf_client, stack_name)
    logical_ids = [b["logical_id"] for b in s3_buckets]
    if not logical_ids:
        print(f"    No S3 resources found in {stack_name}, cannot retry")
        return False

    # Re-empty buckets (new objects may have appeared since first empty)
    for b in s3_buckets:
        if b["physical_id"]:
            print(f"    Re-emptying bucket {b['physical_id']} before retry...")
            empty_bucket(s3_client, b["physical_id"])

    print(f"    Retrying {stack_name} with RetainResources={logical_ids}...")
    cf_client.delete_stack(StackName=stack_name, RetainResources=logical_ids)
    if not wait_for_delete(cf_client, stack_name):
        print(f"    Retry also failed for {stack_name}")
        return False

    # Stack deleted but buckets retained — clean them up
    for b in s3_buckets:
        if b["physical_id"]:
            print(f"    Cleaning up retained bucket: {b['physical_id']}")
            empty_bucket(s3_client, b["physical_id"])
            delete_bucket(s3_client, b["physical_id"])

    return True


def main():
    parser = argparse.ArgumentParser(description="Nuke aws-agent-test stacks")
    parser.add_argument("--yes", "-y", action="store_true", help="Skip confirmation")
    args = parser.parse_args()

    cf = boto3.client("cloudformation", region_name=REGION)
    s3 = boto3.client("s3", region_name=REGION)

    print("==> Finding all aws-agent-test stacks...")
    stacks = get_stacks(cf)

    if not stacks:
        print("    No stacks found.")
        return

    print("Found stacks:")
    for s in stacks:
        print(f"    {s}")

    if not args.yes:
        confirm = input("Delete ALL of these? (y/N) ").strip().lower()
        if confirm not in ("y", "yes"):
            print("Aborted.")
            return

    # Separate bootstrap from scenario stacks
    bootstrap = f"{STACK_PREFIX}-bootstrap"
    scenario_stacks = [s for s in stacks if s != bootstrap]
    has_bootstrap = bootstrap in stacks
    failed = []

    # Delete scenario stacks first
    for stack in scenario_stacks:
        print(f"==> Deleting {stack}...")
        cf.delete_stack(StackName=stack)

    # Wait for scenario stacks
    for stack in scenario_stacks:
        print(f"    Waiting for {stack}...")
        if not wait_for_delete(cf, stack):
            failed.append(stack)

    # Delete bootstrap last (has the S3 bucket)
    if has_bootstrap:
        # Find and empty the artifact bucket before deleting the stack
        bucket = None
        print("==> Emptying artifact bucket before deleting bootstrap stack...")
        try:
            resp = cf.describe_stacks(StackName=bootstrap)
            outputs = resp["Stacks"][0].get("Outputs", [])
            bucket = next(
                (o["OutputValue"] for o in outputs if o["OutputKey"] == "ArtifactBucketName"),
                None,
            )
            if bucket:
                empty_bucket(s3, bucket)
            else:
                print("    No ArtifactBucketName output found, skipping bucket empty")
        except ClientError as e:
            print(f"    Warning: could not find/empty bucket: {e}")

        if not delete_stack_with_retry(cf, s3, bootstrap):
            failed.append(bootstrap)

    if failed:
        print(f"==> WARNING: {len(failed)} stack(s) failed to delete: {', '.join(failed)}")
        sys.exit(1)
    else:
        print("==> All stacks deleted.")


if __name__ == "__main__":
    main()
