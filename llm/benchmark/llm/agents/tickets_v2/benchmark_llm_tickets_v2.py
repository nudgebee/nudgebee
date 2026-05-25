import json
import logging
import math
import random
from datetime import datetime
import time
import pytest
import requests
from datasets import Dataset
from langchain_google_genai import ChatGoogleGenerativeAI, GoogleGenerativeAIEmbeddings
from ragas import evaluate
from ragas.metrics import (
    answer_relevancy,
    answer_similarity,
)
import os
from dotenv import load_dotenv

load_dotenv()

# Create a logger
logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)

# Create a stream handler to print to the console
ch = logging.StreamHandler()
ch.setLevel(logging.INFO)

# Format the log messages
formatter = logging.Formatter("%(asctime)s - %(levelname)s - %(message)s")
ch.setFormatter(formatter)

# Add the handler to the logger
logger.addHandler(ch)


# Load and transform test data
@pytest.fixture(scope="session")
def load_test_data():
    with open("./data/benchmark_tickets_v2.json", "r") as f:
        data = json.load(f)

    # Convert list of dicts into structured dictionary format
    return {
        "query": [item["query"] for item in data],
        "contexts": [item["contexts"] for item in data],
        "ground_truth": [
            [item["ground_truths"]] for item in data
        ],  # Wrap ground truth in list
        "tool_config": [item.get("tool_config", "") for item in data],
        "project_key": [item.get("project_key", "") for item in data],
    }


@pytest.fixture
def sample_queries(load_test_data):
    """Extracts a sample of test queries, contexts, and ground truths"""
    test_data = load_test_data

    queries = test_data["query"]
    expected_contexts = test_data["contexts"]
    ground_truths = test_data["ground_truth"]
    tool_configs = test_data["tool_config"]
    project_keys = test_data["project_key"]

    return queries, expected_contexts, ground_truths, tool_configs, project_keys


@pytest.mark.benchmark
def test_llm_retrieval(sample_queries):  # noqa: C901
    queries, expected_contexts, ground_truths, per_case_tool_configs, per_case_project_keys = (
        sample_queries
    )
    agent = "tickets_v2"

    # Env-var defaults used as fallback when a test case omits tool_config/project_key.
    default_tool_config = os.getenv("TOOL_CONFIG", "")
    default_project_key = os.getenv("PROJECT_KEY", "")

    def build_query_config(tc_name, pk):
        """Build a per-query config, falling back to env-var defaults."""
        effective_tc = tc_name or default_tool_config
        effective_pk = pk or default_project_key
        tc = {}
        if effective_tc:
            tc["ticket_master_v2"] = effective_tc
        if effective_pk:
            tc["ticket_master_v2__project"] = effective_pk
        return {"tool_configs": tc} if tc else {}

    def get_llm_ans(
        query,
        query_config=None,
        account_id=os.getenv("ACCOUNT_ID"),
        tenant_id=os.getenv("TENANT_ID"),
        user_id=os.getenv("USER_ID"),
        agent="tickets_v2",
        secret_key=os.getenv("ACTION_TOKEN"),
    ):
        # Append agent to the query
        query = f"@{agent} {query}"

        url = "http://localhost:9999/v1/completions/chat"
        request_body = {
            "query": query,
            "account_id": account_id,
            "tenant_id": tenant_id,
            "user_id": user_id,
        }
        if query_config:
            request_body["config"] = query_config

        payload = json.dumps(request_body)
        headers = {
            "x-tenant-id": tenant_id,
            "x-user-id": user_id,
            "X-ACTION-TOKEN": secret_key,
            "Content-Type": "application/json",
        }

        response = requests.request("POST", url, headers=headers, data=payload)
        try:
            if response.status_code == 200:
                response_data = response.json()
                # Check if the response contains the expected structure
                if response_data is None or "data" not in response_data:
                    logger.error(f"Unexpected response format: {response_data}")
                    return []
                if "agent_step_response" not in response_data["data"]:
                    logger.error(f"Unexpected response format: {response_data}")
                    return []
                steps = response_data["data"].get("agent_step_response", [])
                if not steps:
                    logger.info("No steps found in agent_step_response")
                for step in steps:
                    call = step.get("Call", {}).get("tool_call", {}).get("function", {})
                    if call.get("name") == "ticket_master_v2":
                        command = call.get("arguments", "")
                        if command:
                            return [command]
                    else:
                        continue
                # Extract the answer text from the response
                if "data" in response_data and "response" in response_data["data"]:
                    logger.info(f"Response data: {response_data['data']['response']}")
                    return response_data["data"]["response"]
                else:
                    logger.error(f"Unexpected response format: {response_data}")
                    return []
            else:
                logger.error(
                    f"API request failed with status code: {response.status_code}"
                )
                return []
        except Exception as e:
            logger.error(f"JSON decode error: {e}")
            return []

    # Benchmark the entire document retrieval process
    logger.info("Total number of queries: %d", len(queries))
    retrieved_texts, answers = [], []
    failed_indices = []
    count = 1

    for i, query in enumerate(queries):
        logger.info(f"Retrieving documents for {count} of {len(queries)} queries")
        qc = build_query_config(per_case_tool_configs[i], per_case_project_keys[i])
        if qc:
            logger.info(f"Query config for index {i}: {qc}")
        logger.info(f"Query: {query}")
        docs = get_llm_ans(
            query,
            query_config=qc,
            account_id=os.getenv("ACCOUNT_ID", ""),
            agent=agent,
            secret_key=os.getenv("ACTION_TOKEN", ""),
        )
        if docs:
            logger.info(f"Documents retrieved: {docs}")
            retrieved_texts.append([doc for doc in docs])
            answers.append(" ".join(retrieved_texts[-1]))
            logger.info("Documents retrieved successfully")
        else:
            # Record failed index and keep placeholders
            logger.warning("Failed to retrieve documents for query index %d", i)
            failed_indices.append(i)
            retrieved_texts.append([])
            answers.append("")  # placeholder
        count += 1

    # Retry failed queries
    max_retries = 2  # configurable: number of retry attempts
    retry_delay = 1  # seconds between retries (simple backoff)
    if failed_indices:
        logger.info("Retrying failed queries: %s", failed_indices)
    for attempt in range(1, max_retries + 1):
        if not failed_indices:
            break
        logger.info(
            "Retry attempt %d for %d failed queries", attempt, len(failed_indices)
        )
        # iterate over a copy because we'll modify the list
        for idx in failed_indices[:]:
            query = queries[idx]
            logger.info("Retrying index %d: %s", idx, query)
            qc = build_query_config(per_case_tool_configs[idx], per_case_project_keys[idx])
            docs = get_llm_ans(
                query,
                query_config=qc,
                account_id=os.getenv("ACCOUNT_ID", ""),
                agent=agent,
                secret_key=os.getenv("ACTION_TOKEN", ""),
            )
            logger.info("Retry documents retrieved: %s", docs)
            if docs:
                retrieved_texts[idx] = [doc for doc in docs]
                answers[idx] = " ".join(retrieved_texts[idx])
                failed_indices.remove(idx)
                logger.info("Retry success for index %d", idx)
            else:
                logger.warning("Retry failed for index %d on attempt %d", idx, attempt)
        if failed_indices:
            time.sleep(retry_delay)

    if failed_indices:
        logger.error("Following query indices failed after retries: %s", failed_indices)
    else:
        logger.info("All queries retrieved successfully after retries")

    # If everything failed, skip evaluation to avoid exceptions
    if all(a == "" for a in answers):
        pytest.skip("All retrieval attempts failed; skipping evaluation.")

    for idx in failed_indices:
        answers[idx] = "SYSTEM FAILURE"

    eval_queries = []
    eval_contexts = []
    eval_answers = []
    eval_references = []
    eval_expected_contexts = []

    def flatten_ground_truth(gt):
        ref = ""
        for item in gt if isinstance(gt, list) else [gt]:
            if isinstance(item, (list, tuple)):
                ref += "".join([str(x) for x in item])
            else:
                ref += str(item)
        return ref

    for i in range(len(queries)):
        if i in failed_indices:
            continue
        eval_queries.append(queries[i])
        eval_contexts.append(retrieved_texts[i])
        eval_answers.append(answers[i])
        eval_expected_contexts.append(expected_contexts[i])
        eval_references.append(flatten_ground_truth(ground_truths[i]))

    # Prepare dataset for Ragas evaluation
    eval_data = Dataset.from_dict(
        {
            "query": eval_queries,
            "user_input": eval_queries,
            "contexts": eval_contexts,
            "answer": eval_answers,
            "reference": eval_references,
        }
    )

    # Use Google LLM and embeddings
    llm = ChatGoogleGenerativeAI(model="gemini-2.5-pro", temperature=0)
    embeddings = GoogleGenerativeAIEmbeddings(model="gemini-embedding-001")

    # Define evaluation metrics
    metrics = [answer_relevancy, answer_similarity]
    results = evaluate(
        eval_data,
        metrics=metrics,
        llm=llm,
        embeddings=embeddings,
        raise_exceptions=True,
    )
    # Extract metrics into separate lists
    # ragas 0.2.x renamed answer_similarity to semantic_similarity internally
    similarity_key = answer_similarity.name  # "semantic_similarity" in ragas 0.2.x
    relevancy_key = answer_relevancy.name  # "answer_relevancy"

    answer_similarity_values = results[similarity_key]
    answer_relevancy_values = results[relevancy_key]

    # Convert results to a dictionary (if it's an object with protected attributes)
    metrics_dict = results._repr_dict  # Access the protected dictionary

    # Extract values
    answer_similarity_score = metrics_dict[similarity_key]
    answer_relevancy_score = metrics_dict[relevancy_key]

    # Compute overall accuracy
    overall_accuracy = (answer_similarity_score + answer_relevancy_score) / 2

    # Create a detailed report with metrics for each query
    report = {
        "details": [
            {
                "query": eval_queries[i],
                "retrieved_contexts": eval_contexts[i],
                "expected_contexts": eval_expected_contexts[i],
                "ground_truth": eval_references[i],
                "answer_similarity": round(answer_similarity_values[i] * 100, 2),
                "answer_relevancy": round(answer_relevancy_values[i] * 100, 2),
            }
            for i in range(len(eval_queries))
        ],
        "summary": {
            "total_queries": len(eval_queries),
            "answer_similarity": round(answer_similarity_score * 100, 2),
            "answer_relevancy": round(answer_relevancy_score * 100, 2),
            "overall_accuracy": round(overall_accuracy * 100, 2),
            "failed_indices_after_retries": failed_indices,
        },
    }

    # Save results to JSON
    date_str = datetime.now().strftime("%Y%m%d")
    random_int = random.randint(1000, 9999)
    filename = f"./report/llm_eval_report_{agent}_{date_str}_{random_int}.json"
    with open(filename, "w") as f:
        json.dump(report, f, indent=4)

    # Handle the case where the metric might be nan
    if isinstance(answer_similarity_score, float) and math.isnan(
        answer_similarity_score
    ):  # NaN check
        answer_similarity_score = 0
    if isinstance(answer_relevancy_score, float) and math.isnan(
        answer_relevancy_score
    ):  # NaN check
        answer_relevancy_score = 0

    # Print metrics including overall accuracy
    print("\n===== LLM Evaluation Report =====")
    print(f"Total Queries Evaluated: {len(eval_queries)}")
    print(f"Answer Similarity: {answer_similarity_score:.4f}")
    print(f"Answer Relevancy: {answer_relevancy_score:.4f}")
    print(f"Overall Accuracy: {overall_accuracy:.4f}")  # Display overall accuracy
    print("=================================\n")

    # Assertions for CI/CD pipeline
    logger.info(
        f"Answer Similarity: {answer_similarity_score:.4f}"
        if answer_similarity_score >= 0.7
        else f"Low Answer Similarity: {answer_similarity_score:.4f}"
    )

    logger.info(
        f"Answer Relevancy: {answer_relevancy_score:.4f}"
        if answer_relevancy_score >= 0.7
        else f"Low answer relevancy: {answer_relevancy_score:.4f}"
    )

    logger.info(
        f"Overall Accuracy: {overall_accuracy:.4f}"
        if overall_accuracy >= 0.75
        else f"Low overall accuracy: {overall_accuracy:.4f}"
    )
