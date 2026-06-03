###########################################
# Cross Account IAM Role
###########################################
resource "aws_iam_role" "cross_account_role" {
  name = "nudgebee-cross-account-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17",
    Statement = [{
      Effect = "Allow",
      Principal = { AWS = var.nudgebee_iam_role },
      Action = "sts:AssumeRole"
    }]
  })

  managed_policy_arns = [
    "arn:aws:iam::aws:policy/job-function/ViewOnlyAccess"
  ]
}

###########################################
# Nudgebee Billing Read-Only Policy
###########################################
resource "aws_iam_role_policy" "billing_readonly" {
  name = "NudgebeeBillingReadOnly"
  role = aws_iam_role.cross_account_role.id

  policy = jsonencode({
    Version = "2012-10-17",
    Statement = [{
      Sid      = "NudgebeeBillingReadOnly",
      Effect   = "Allow",
      Action   = [
        "aws-portal:View*",
        "budgets:Describe*",
        "budgets:View*",
        "ce:Get*",
        "ce:Describe*",
        "ce:List*",
        "cur:Describe*",
        "pricing:*",
        "organizations:Describe*",
        "organizations:List*",
        "savingsplans:Describe*"
      ],
      Resource = "*"
    }]
  })
}

###########################################
# Observability (CloudWatch, Logs, X-Ray)
###########################################
resource "aws_iam_role_policy" "observability_readonly" {
  name = "NudgebeeObservabilityReadOnly"
  role = aws_iam_role.cross_account_role.id

  policy = jsonencode({
    Version = "2012-10-17",
    Statement = [
      {
        Sid    = "LogsAccess",
        Effect = "Allow",
        Action = [
          "logs:StartQuery",
          "logs:StopQuery",
          "logs:Describe*",
          "logs:List*",
          "logs:Get*",
          "logs:Filter*"
        ],
        Resource = "arn:aws:logs:*:*:log-group:*:*"
      },
      {
        Sid      = "MetricsAccess",
        Effect   = "Allow",
        Action   = [
          "cloudwatch:Describe*",
          "cloudwatch:Get*",
          "cloudwatch:List*"
        ],
        Resource = "*"
      },
      {
        Sid      = "XrayAccess",
        Effect   = "Allow",
        Action   = [
          "xray:BatchGet*",
          "xray:GetService*",
          "xray:GetTrace*"
        ],
        Resource = "*"
      }
    ]
  })
}

###########################################
# Additional Read-Only AWS Resources
###########################################
resource "aws_iam_role_policy" "additional_readonly" {
  name = "NudgebeeAdditionalResourceReadOnly"
  role = aws_iam_role.cross_account_role.id

  # This JSON is large — truncated for brevity but matches your CFN exactly.
  policy = file("${path.module}/additional_readonly_policy.json")
}

###########################################
# Cost and Usage Report Bucket
###########################################
resource "aws_s3_bucket" "cost_usage_bucket" {
  bucket = var.bucket_name

  lifecycle_rule {
    id      = "ExpireOldObjects"
    enabled = true
    expiration { days = 200 }
  }

  tags = {
    Name        = var.bucket_name
    Environment = "production"
  }
}

###########################################
# S3 Bucket Policy
###########################################
resource "aws_s3_bucket_policy" "cost_usage_policy" {
  bucket = aws_s3_bucket.cost_usage_bucket.id

  policy = jsonencode({
    Version = "2012-10-17",
    Statement = [
      {
        Sid    = "AllowBillingService",
        Effect = "Allow",
        Principal = { Service = "billingreports.amazonaws.com" },
        Action = ["s3:GetBucketAcl", "s3:GetBucketPolicy", "s3:GetLifecycleConfiguration"],
        Resource = aws_s3_bucket.cost_usage_bucket.arn
      },
      {
        Sid    = "AllowBillingPut",
        Effect = "Allow",
        Principal = { Service = "billingreports.amazonaws.com" },
        Action   = ["s3:PutObject"],
        Resource = "${aws_s3_bucket.cost_usage_bucket.arn}/*"
      },
      {
        Sid    = "AllowNudgebeeRoleGet",
        Effect = "Allow",
        Principal = { AWS = aws_iam_role.cross_account_role.arn },
        Action = ["s3:GetObject", "s3:GetObjectAcl"],
        Resource = "${aws_s3_bucket.cost_usage_bucket.arn}/*"
      }
    ]
  })
}

###########################################
# Object Get Policy for Role
###########################################
resource "aws_iam_role_policy" "object_get_policy" {
  name = "ObjectGetCostUsageReports"
  role = aws_iam_role.cross_account_role.id

  policy = jsonencode({
    Version = "2012-10-17",
    Statement = [
      {
        Effect = "Allow",
        Action = ["s3:GetObject", "s3:GetObjectAcl"],
        Resource = "${aws_s3_bucket.cost_usage_bucket.arn}/*"
      },
      {
        Effect = "Allow",
        Action = ["s3:GetLifecycleConfiguration"],
        Resource = aws_s3_bucket.cost_usage_bucket.arn
      }
    ]
  })
}
