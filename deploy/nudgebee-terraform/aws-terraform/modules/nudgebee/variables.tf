variable "nudgebee_iam_role" {
  type        = string
  description = "The Nudgebee IAM role allowed to assume this role"
}

variable "bucket_name" {
  type        = string
  description = "The name of the S3 bucket to store reports"
}

variable "report_name" {
  type        = string
  description = "The name of the Cost and Usage Report"
}

variable "nudgebee_id" {
  type        = string
}

variable "nudgebee_domain" {
  type        = string
}

variable "nudgebee_user_id" {
  type        = string
}

variable "nudgebee_account_name" {
  type        = string
}
