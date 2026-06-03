terraform {
  required_version = ">= 1.6.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

module "nudgebee" {
  source = "./modules/nudgebee"

  nudgebee_iam_role      = var.nudgebee_iam_role
  bucket_name            = var.bucket_name
  report_name            = var.report_name
  nudgebee_id            = var.nudgebee_id
  nudgebee_domain        = var.nudgebee_domain
  nudgebee_user_id       = var.nudgebee_user_id
  nudgebee_account_name  = var.nudgebee_account_name
}
