variable "nudgebee_iam_role" {
  description = "The Nudgebee IAM role allowed to assume this role"
  type        = string
}

variable "bucket_name" {
  description = "The name of the S3 bucket to store reports"
  type        = string
}

variable "report_name" {
  description = "The name of the Cost and Usage Report"
  type        = string
}

variable "nudgebee_id" {
  description = "Nudgebee customer ID"
  type        = string
}

variable "nudgebee_domain" {
  description = "Nudgebee domain"
  type        = string
}

variable "nudgebee_user_id" {
  description = "Nudgebee user ID"
  type        = string
}

variable "nudgebee_account_name" {
  description = "Nudgebee account name"
  type        = string
}

variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "ap-south-1"
}
