import CustomTable from '@components1/common/tables/CustomTable2';

export function buildDescriptionMarkdown(details: Record<string, any> | undefined): string {
  if (!details) {
    return '';
  }
  const parts: string[] = [];

  if (details.description) {
    parts.push(details.description);
  }

  if (details.recommendations?.length) {
    parts.push('#### Recommendation');
    parts.push(details.recommendations.join('\n\n'));
  }

  if (details.compliances?.length) {
    parts.push('#### Compliance');
    parts.push(details.compliances.join(', '));
  }

  if (details.references?.length) {
    parts.push('#### References');
    details.references.forEach((url: string) => {
      const label = url.replace(/https?:\/\//, '').replace(/\/$/, '');
      parts.push(`- [${label}](${url})`);
    });
  }

  return parts.join('\n\n');
}

/**
 * For Azure Advisor recommendations, the static mitigation text is generic since Advisor covers
 * many different recommendation types. This function builds a dynamic mitigation from the actual
 * recommendation data (solution, description, learn_more_link, resource_path, etc.).
 */
function buildAzureAdvisorMitigation(recommendation: Record<string, any>): string[] | null {
  const ruleName = recommendation.rule_name || '';
  if (!ruleName.startsWith('azure_native_advisor_')) return null;

  const data = recommendation.recommendation;
  if (!data || typeof data !== 'object') return null;

  const solution = data.solution;
  const description = data.description;
  const learnMoreLink = data.learn_more_link;
  const resourcePath = data.resource_path;
  const potentialBenefits = data.potential_benefits;

  // If there's no useful data, keep the generic text
  if (!solution && !description) return null;

  const parts: string[] = [];

  if (solution) {
    parts.push(`#### Recommended Action\n${solution}`);
  }

  if (description && description !== solution) {
    parts.push(`#### Details\n${description}`);
  }

  if (potentialBenefits) {
    parts.push(`**Potential Benefits:** ${potentialBenefits}`);
  }

  if (resourcePath) {
    parts.push(`**Impacted Resource:** \`${resourcePath}\``);
  }

  // Build Azure Portal link if we have subscription and recommendation ID
  const subId = data.ext_subid || data.subscription_id;
  if (subId) {
    parts.push(`**Azure Portal:** [Open Advisor Recommendations](https://portal.azure.com/#blade/Microsoft_Azure_Expert/AdvisorMenuBlade/overview)`);
  }

  if (learnMoreLink) {
    parts.push(`**Learn More:** [${learnMoreLink.replace(/https?:\/\//, '').replace(/\/$/, '')}](${learnMoreLink})`);
  }

  return parts.length > 0 ? [parts.join('\n\n')] : null;
}

export function interpolateMitigations(mitigations: string[] | undefined, recommendation: Record<string, any> | undefined): string[] | undefined {
  if (!mitigations || !recommendation) {
    return mitigations;
  }

  // For Azure Advisor recommendations with generic mitigations, try to build dynamic content
  const ruleName = recommendation.rule_name || '';
  if (ruleName.startsWith('azure_native_advisor_')) {
    const isGeneric = mitigations.some((m) => m.includes('Follow the specific remediation steps provided'));
    if (isGeneric) {
      const dynamicMitigations = buildAzureAdvisorMitigation(recommendation);
      if (dynamicMitigations) return dynamicMitigations;
    }
  }

  return mitigations.map((m) =>
    m.replace(/\{\{(\w+(?:\.\w+)*)\}\}/g, (match, path) => {
      if (path === 'region') {
        const parts = recommendation.account_object_id?.split(':');
        return parts?.[3] || match;
      }
      if (path === 'resource_group') {
        // Try extracting from resource_id first (may be an Azure resource path),
        // then fall back to account_object_id which contains the full Azure resource path
        const rid = recommendation.resource_id || '';
        const rgMatch = rid.match(/resourceGroups\/([^/]+)/i);
        if (rgMatch?.[1]) return rgMatch[1];
        const aoid = recommendation.account_object_id || '';
        const aoMatch = aoid.match(/resourcegroups\/([^/]+)/i);
        return aoMatch?.[1] || match;
      }
      const segments = path.split('.');
      let value: any = recommendation;
      for (const seg of segments) {
        if (value == null) {
          break;
        }
        value = value[seg];
      }
      return value != null && value !== '' ? String(value) : match;
    })
  );
}

export const recommendationDetails = {
  Configuration: {
    aws_tags: {
      title: 'AWS Resources Should Have Tags',
      description: `As your AWS cloud environment is becoming more and more complex, it requires better management strategies. Using a tagging schema will help you gain visibility over your cloud resources and organize them more efficiently. You can use tags for different scenarios such as tracking resources owners and their stack level, identify which resources are incurring the highest costs, and filter available resources based on particular deployment stage.`,
      recommendations: [
        `Ensure that user-defined tags (metadata) are being used for labeling, collecting, and organizing resources available within your AWS cloud environment. It is recommended that you tag your resources with appropriate metadata to help you manage and organize your resources.
                It is recommended to have tags like Name, Environment, Owner and Role to have better track of resources.`,
      ],
      mitigations: [
        'Run describe-instances command (OSX/Linux/UNIX) using the ID of the Amazon EC2 instance that you want to configure as the identifier parameter to apply the specified tagging schema (i.e. Name, Role, Environment, and Owner) to the selected EC2 instance (the command does not produce an output): ```aws ec2 create-tags --resources {{recommendation.instance_id}} --tags Key=Name,Value=Prod-Web-Server Key=Role,Value=Web-Tier Key=Environment,Value=Production Key=Owner,Value=DevOps-Team```',
      ],
      compliances: ['APRA', 'MAS', 'NIST4'],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Using_Tags.html'],
    },
    aws_ec2_instance_auto_termination: {
      title: 'EC2 Instance Termination Protection Should Be Enabled',
      description: `By default, the EBS volumes associated with the Amazon EC2 instances are deleted when these are terminated (when the DeletionOnTermination attribute value is set to true). With Termination Protection feature enabled, you have the guarantee that your EC2 instances can't be terminated accidentally and make sure that your data remains safe.`,
      serviceName: 'AmazonEC2',
      recommendations: [
        `
        Ensure that the Amazon EC2 instances provisioned outside the Auto Scaling Groups (ASGs) have Termination Protection safety feature enabled in order to protect them from being accidentally terminated.
        For Amazon EC2 instances provisioned manually, once the Termination Protection feature is enabled you will not be able to terminate your EC2 instances using the AWS Management Console, the AWS API, or the AWS CLI until the Termination Protection has been disabled. However, this will not prevent your instances from getting terminated if these have set the Shutdown Behavior flag to 'Terminate' when an OS-level shutdown is performed. To make sure your EC2 instances cannot be accidentally terminated, you need to set first the instance Shutdown Behavior value to 'Stop' (which sets the InstanceInitiatedShutdownBehavior attribute value to 'stop') then enable Termination Protection safety feature (which sets the DisableApiTermination attribute value to true).
        For Amazon EC2 instances provisioned automatically via AWS CloudFormation, once the Termination Protection feature is enabled, you will not be able to delete the stack containing the instance until the feature has been disabled (which sets the DisableApiTermination attribute value to false) in your CloudFormation template.
        `,
      ],
      mitigations: [
        `Run modify-instance-attribute command (OSX/Linux/UNIX) using the ID of the Amazon EC2 instance that you want to protect against accidental termination as the identifier parameter, to enable the Termination Protection safety feature for the selected EC2 instance (if successful, the command does not produce an output): 
\`\`\`
aws ec2 modify-instance-attribute
--region {{region}}
--instance-id {{recommendation.instance_id}}
--disable-api-termination
\`\`\`
`,
        `**Terraform configuration file (.tf):**
\`\`\`
      terraform {
            required_providers {
                aws = {
                    source  = "hashicorp/aws"
                    version = "~> 4.0"
                }
            }
            required_version = ">= 0.14.9"
        }
        provider "aws" {
            profile = "default"
            region  = "us-east-1"
        }
        resource "aws_instance" "ec2-instance" {
            ami = "ami-0123456789abcdefa"
            instance_type = "c5.xlarge"
            key_name = "ssh-key"
            subnet_id = "subnet-0123456789abcdef0"
            vpc_security_group_ids = [ "sg-0123456789abcdefa" ]
            ebs_block_device {
                device_name = "/dev/xvda"
                volume_size = 30
                volume_type = "gp2"
            }

            disable_api_termination = true
        }
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Using_ChangingDisableAPITermination.html'],
    },
    aws_eks_logging: {
      title: 'Enable CloudTrail Logging for Kubernetes API Calls',
      description:
        'Enabling CloudTrail logging for Amazon EKS clusters is vital for security monitoring, compliance adherence, incident investigation, and operational insights. It provides a detailed audit trail of API calls, enabling proactive detection of unauthorized access, ensuring regulatory compliance, facilitating incident response, and optimizing cluster performance and change management.',
      serviceName: 'AmazonEKS',
      recommendations: [
        `Ensure that CloudTrail logging is enabled for Amazon Elastic Kubernetes Service (EKS) clusters in order to record all Kubernetes API calls. Amazon CloudTrail records and documents all activities performed on EKS clusters. Whenever operations such as "CreateCluster," "ListClusters," or "DeleteCluster" are executed, corresponding records are generated in the CloudTrail trail log files. Each event or log entry includes details about the IAM identity responsible for the request and the credentials utilized.`,
      ],
      mitigations: [
        `Update your cluster's control plane log export configuration with the following AWS CLI command:
        \`\`\`
        aws eks update-cluster-config \
        --region {{region}} \
        --name {{resource_name}} \
        --logging '{"clusterLogging":[{"types":["api","audit","authenticator","controllerManager","scheduler"],"enabled":true}]}
        \`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/eks/latest/userguide/control-plane-logs.html'],
    },
    aws_ecr_tag_immutable: {
      title: 'Ecr Image Tags should be immutable',
      description:
        'ECR images should be set to IMMUTABLE to prevent code injection through image mutation. This can be done by setting image_tab_mutability to IMMUTABLE',
      serviceName: 'AmazonECR',
      recommendations: [
        `ECR images should be set to IMMUTABLE to prevent code injection through image mutation. This can be done by setting image_tab_mutability to IMMUTABLE`,
      ],
      mitigations: [
        `AWS CLI command to set image tag mutability to IMMUTABLE:
\`\`\`
aws ecr put-image-tag-mutability --repository-name name --image-tag-mutability IMMUTABLE --region us-east-2
\`\`\`
`,
        `**Terraform configuration file (.tf):**
 \`\`\`resource "aws_ecr_repository" "good_example" {
        name                 = "bar"
        image_tag_mutability = "IMMUTABLE"

        image_scanning_configuration {
            scan_on_push = true
        }
 }
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonECR/latest/userguide/image-tag-mutability.html'],
    },
    aws_lambda_dead_letter_queue: {
      title: 'Enable Dead Letter Queue for Lambda Functions',
      description: 'Ensure there is a Dead Letter Queue configured for each Lambda function available in your AWS account.',
      serviceName: 'AWSLambda',
      recommendations: [
        'When an event fails all attempts or stays in the asynchronous invocation queue for too long, Amazon Lambda service discards it. Enabling Dead Letter Queues (DLQs) for your Amazon Lambda functions can make your serverless application more resilient by capturing and storing unprocessed events from asynchronous invocations for further analysis or reprocessing. Configuring Dead Letter Queues for Amazon Lambda functions will give you more control over message handling for all asynchronous invocations, including those delivered via AWS service events (S3, SNS, IoT, etc.).',
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                    source  = "hashicorp/aws"
                    version = "~> 4.0"
                }
            }

            required_version = ">= 0.14.9"
        }

        provider "aws" {
            profile = "default"
            region  = "us-east-1"
        }

        resource "aws_iam_role" "lambda-execution-role" {
            name = "LambdaExecutionRole"
            path = "/"
            managed_policy_arns = ["arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"]

            assume_role_policy = <<EOF
            {
                "Version": "2012-10-17",
                "Statement": [
                    {
                        "Action": "sts:AssumeRole",
                        "Principal": {
                            "Service": "lambda.amazonaws.com"
                        },
                        "Effect": "Allow"
                    }
                ]
            }
            EOF
        }

        resource "aws_sqs_queue" "lambda-function-dlq" {
            name                       = "cc-lambda-function-dlq"
            message_retention_seconds  = 3600
            visibility_timeout_seconds = 10
        }

        resource "aws_lambda_function" "lambda-function" {
            function_name    = "cc-app-worker-function"
            s3_bucket        = "cc-lambda-functions"
            s3_key           = "worker.zip" 
            role             = aws_iam_role.lambda-execution-role.arn
            handler          = "lambda_function.lambda_handler"
            runtime          = "python3.9"
            memory_size      = 1024
            timeout          = 45   
            vpc_config {
                subnet_ids         = [ "subnet-01234abcd1234abcd", "subnet-0abcd1234abcd1234" ]
                security_group_ids = [ "sg-0abcd1234abcd1234" ]
            }
            dead_letter_config = {
                target_arn = aws_sqs_queue.lambda-function-dlq.arn
            }
        }
\`\`\`
`,
        `AWS CLI command to enable Dead Letter Queue for Lambda function:
Create a Dead Letter Queue (DLQ) in Amazon SQS:

\`\`\`
        aws sqs create-queue
        --region {{region}}
        --queue-name cc-dead-letter-queue
\`\`\`


        Update the Lambda function configuration to use the Dead Letter Queue:
\`\`\`
        aws lambda update-function-configuration
        --region {{region}}
        --function-name {{resource_id}}
        --dead-letter-config TargetArn=arn:aws:sqs:us-east-1:123456789012:cc-dead-letter-queue
\`\`\`
`,
      ],
      references: ['https://aws.amazon.com/about-aws/whats-new/2016/12/aws-lambda-supports-dead-letter-queues/'],
    },
    aws_lambda_provisioned_concurrency: {
      title: 'Enable and Configure Provisioned Concurrency',
      description: `When you are using Lambda functions for your serverless application, you are not provisioning hardware or networks, runtimes and operating systems, but you are able to enable and configure features such as provisioned concurrency which can directly affect your application's performance. Functions configured with provisioned concurrency can execute with consistent start-up latency, making them ideal for building interactive web and mobile back-ends, latency sensitive microservices, and synchronously invoked APIs.`,
      serviceName: 'AWSLambda',
      recommendations: [
        'Ensure that the Provisioned Concurrency performance feature is enabled for your Amazon Lambda functions in order to help your functions to scale without fluctuations in latency. Provisioned concurrency runs continuously and has a separate pricing plan for concurrency and execution duration.',
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                    source  = "hashicorp/aws"
                    version = "~> 4.0"
                }
            }

            required_version = ">= 0.14.9"
        }

        provider "aws" {
            profile = "default"
            region  = "us-east-1"
        }

        resource "aws_iam_role" "lambda-execution-role" {
            name = "LambdaExecutionRole"
            path = "/"
            managed_policy_arns = ["arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"]

            assume_role_policy = <<EOF
            {
                "Version": "2012-10-17",
                "Statement": [
                    {
                        "Action": "sts:AssumeRole",
                        "Principal": {
                            "Service": "lambda.amazonaws.com"
                        },
                        "Effect": "Allow"
                    }
                ]
            }
            EOF
        }

        resource "aws_lambda_function" "lambda-function" {
            function_name    = "cc-app-worker-function"
            s3_bucket        = "cc-lambda-functions"
            s3_key           = "worker.zip" 
            role             = aws_iam_role.lambda-execution-role.arn
            handler          = "lambda_function.lambda_handler"
            runtime          = "python3.9"
            memory_size      = 1024
            timeout          = 45   
            vpc_config {
                subnet_ids         = [ "subnet-01234abcd1234abcd", "subnet-0abcd1234abcd1234" ]
                security_group_ids = [ "sg-0abcd1234abcd1234" ]
            }
        }

        resource "aws_lambda_alias" "lambda-function-alias" {
            function_name    = aws_lambda_function.lambda-function.function_name
            function_version = aws_lambda_function.lambda-function.version
            name             = "cc-app-worker"
        }

        resource "aws_lambda_provisioned_concurrency_config" "lambda-provisioned-concurrency" {
            function_name                     = aws_lambda_alias.lambda-function-alias.function_name
            provisioned_concurrent_executions = 300
            qualifier                         = aws_lambda_alias.lambda-function-alias.name
        }
\`\`\`
`,
        `AWS CLI command to enable Provisioned Concurrency for Lambda function:
\`\`\`
        aws lambda put-provisioned-concurrency-config
        --region {{region}}
        --function-name cc-app-worker-function
        --qualifier cc-app-worker
        --provisioned-concurrent-executions 300
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/lambda/latest/dg/provisioned-concurrency.html'],
    },
    aws_lambda_reserved_concurrency: {
      title: 'Enable and Configure Reserved Concurrency For Lambda Functions',
      description: `Reserved concurrency in Amazon Lambda allows you to set a limit on the number of concurrent executions for a specific Lambda function, ensuring it doesn't exceed that limit even during spikes in traffic. Setting a concurrent execution limit at the Lambda function level ensures predictable scaling, prevents excessive resource usage, and enhances cost control and performance.`,
      serviceName: 'AWSLambda',
      recommendations: [
        `Ensure that the Reserved Concurrency feature is enabled and configured for your Amazon Lambda functions. Enabling reserved concurrency ensures predictable performance and cost control for your Lambda functions by limiting the number of concurrent executions.`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                    source  = "hashicorp/aws"
                    version = "~> 4.0"
                }
            }
            required_version = ">= 0.14.9"            
        }
\`\`\`
`,
        `AWS CLI command to enable Reserved Concurrency for Lambda function:
\`\`\`
        aws lambda put-function-concurrency 
        --function-name cc-process-stream-function 
        --reserved-concurrent-executions 150
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/lambda/latest/dg/configuration-concurrency.html'],
    },
    aws_lambda_deprecated_runtime: {
      title: 'Lambda Using Supported Runtime Environment',
      description:
        'When you execute your Lambda functions using a supported version of the implemented runtime environment, you ensure your function is not at risk of reaching end of support from AWS. To benefit from new features and enhancements, better security, performance and reliability, it is recommended to update to the latest environment version.',
      serviceName: 'AWSLambda',
      recommendations: [
        `Ensure that your Amazon Lambda function always uses a supported environment version, in order to avoid end of support timeframes from AWS. AWS Phase 1 Deprecation means Lambda functions no longer receive security patches or other updates to the runtime. You can no longer create functions that use the runtime, but you can continue to update existing functions. This includes updating the runtime, and rolling back to the previous runtime. Note that functions that use a deprecated runtime are no longer eligible for technical support. It is recommended to updated to the latest version to adhere to AWS cloud best practices and receive the newest software features, get the latest security patches and bug fixes, and benefit from better performance and reliability. A Lambda runtime (execution) environment is a container build based on the configuration settings that you provide when you create your Lambda function. Amazon Lambda serverless architecture supports several runtime environments such as Node.js, Edge Node.js, Java, Python and .NET Core (C#) that you can use to run your functions.`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                    source  = "hashicorp/aws"
                    version = "~> 3.27"
                }
            }
            required_version = ">= 0.14.9"
        }
        provider "aws" {
            profile = "default"
            region  = "us-east-1"
        }
        resource "aws_iam_role" "function-execution-role" {
            name = "LambdaExecutionRole"
            path = "/"
            managed_policy_arns = [ "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole" ]
            assume_role_policy = <<EOF
            {
                "Version": "2012-10-17",
                "Statement": [
                    {
                        "Action": "sts:AssumeRole",
                        "Principal": {
                            "Service": "lambda.amazonaws.com"
                        },
                        "Effect": "Allow"
                    }
                ]
            }
            EOF
        }
        resource "aws_lambda_function" "lambda-function" {
            function_name    = "cc-app-worker-function"
            s3_bucket        = "cc-lambda-functions"
            s3_key           = "sqs-consumer.zip" 
            role             = aws_iam_role.function-execution-role.arn
            handler          = "lambda_function.lambda_handler"
            memory_size      = 1024
            timeout          = 45   
            # Upgrade the Runtime Environment Version
            runtime          = "python3.11"
        }
\`\`\`
`,
        `AWS CLI command to update the Runtime Environment Version for Lambda function:
\`\`\`
        aws lambda update-function-configuration
        --region {{region}}
        --function-name cc-app-worker-function
        --runtime python3.11
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtimes.html'],
    },
    aws_lambda_tracing: {
      title: 'Tracing Enabled for Lambda Functions',
      description:
        'AWS X-Ray can provide tracing and monitoring capabilities for your Lambda functions. With active tracing mode enabled, you can save time and effort debugging and operating your functions as the X-Ray service support allows you to rapidly diagnose errors, identify bottlenecks, slowdowns and timeouts, by breaking down the latency for your Lambda functions.',
      serviceName: 'AWSLambda',
      recommendations: [
        `Ensure that active tracing is enabled for your Amazon Lambda functions in order to gain visibility into the execution and performance of the functions. With the tracing feature enabled, Amazon activates Lambda support for AWS X-Ray, a service that collects data about requests that your functions perform, which provides tools that you can use to view, filter, and gain insights into the collected data in order to identify issues and opportunities for optimization.`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
            terraform {
            required_providers {
                aws = {
                source  = "hashicorp/aws"
                version = "~> 3.27"
                }
            }

            required_version = ">= 0.14.9"
            }

            provider "aws" {
            profile = "default"
            region  = "us-east-1"
            }

            resource "aws_iam_role" "function-execution-role" {
            name = "LambdaExecutionRole"
            path = "/"
            managed_policy_arns = [ "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole", "arn:aws:iam::aws:policy/AWSXrayWriteOnlyAccess" ]

            assume_role_policy = <<EOF
            {
            "Version": "2012-10-17",
            "Statement": [
                {
                "Action": "sts:AssumeRole",
                "Principal": {
                    "Service": "lambda.amazonaws.com"
                },
                "Effect": "Allow"
                }
            ]
            }
            EOF
            }

            resource "aws_lambda_function" "lambda-function" {
            function_name    = "cc-sqs-poller"
            s3_bucket        = "cc-lambda-functions"
            s3_key           = "sqs-consumer.zip"
            role             = aws_iam_role.function-execution-role.arn
            handler          = "index.handler"
            runtime          = "nodejs12.x"
            memory_size      = 1024
            timeout          = 45

            # Enable Active (X-Ray) Tracing
            tracing_config {
                mode = "Active"
            }

            }
\`\`\`
`,
        `AWS CLI command to enable Active Tracing for Lambda function:
\`\`\`
        aws lambda update-function-configuration
        --region {{region}}
        --function-name cc-sqs-poller
        --tracing-config Mode=Active
\`\`\`
`,
      ],
      references: [
        'https://docs.aws.amazon.com/lambda/latest/dg/services-xray.html',
        'https://docs.aws.amazon.com/lambda/latest/operatorguide/trace-requests.html',
      ],
    },
    aws_rds_reservedinstance_configured: {
      title: 'Reserved Instances Should Be Configured for RDS',
      description: `Amazon RDS Reserved Instances give you the option to reserve a DB instance for a one or three year term and in turn receive a significant discount compared to the On-Demand Instance pricing for the DB instance.
    You can choose between three payment options when you purchase a Reserved Instance. With the All Upfront option, you pay for the entire Reserved Instance with one upfront payment. This option provides you with the largest discount compared to On-Demand Instance pricing. With the Partial Upfront option, you make a low upfront payment and are then charged a discounted hourly rate for the instance for the duration of the Reserved Instance term. The No Upfront option does not require any upfront payment and provides a discounted hourly rate for the duration of the term.
    All Reserved Instance types are available for Aurora, MySQL, MariaDB, PostgreSQL, Oracle and SQL Server database engines.`,
      serviceName: 'AmazonRDS',
      recommendations: [
        `Reserve an Amazon RDS instance to save costs. Reserved Instances provide you with a significant discount (up to 75%) compared to On-Demand Instance pricing. You can reserve a DB instance for a one or three year term and in turn receive a significant discount compared to the On-Demand Instance pricing for the DB instance. You can choose between three payment options when you purchase a Reserved Instance. With the All Upfront option, you pay for the entire Reserved Instance with one upfront payment. This option provides you with the largest discount compared to On-Demand Instance pricing. With the Partial Upfront option, you make a low upfront payment and are then charged a discounted hourly rate for the instance for the duration of the Reserved Instance term. The No Upfront option does not require any upfront payment and provides a discounted hourly rate for the duration of the term.`,
      ],
      mitigations: [],
      references: ['https://aws.amazon.com/rds/reserved-instances/'],
    },
    aws_rds_instance_reserved: {
      title: 'Reserved Instance should be used for RDS Instance',
      description: `When an AWS RDS Reserved Instance is not in use (i.e. does not have an active corresponding instance) the investment made is not exploited. For example, if you reserve a db.m3.medium RDS instance within US West (Oregon) region and you don't provision a database instance with the same class/type, in the same region of the same AWS account or in any other linked AWS accounts within your AWS Organization, the specified RDS RI is considered unused and your investment has a negative return.`,
      serviceName: 'AmazonRDS',
      recommendations: [
        `Ensure that all your AWS RDS Reserved Instances (RI) have corresponding database instances running within the same account or within any AWS accounts members of an AWS Organization (if any). A corresponding database instance is a running RDS instance that matches the reservation parameters such as Region and Instance Type.`,
      ],
      mitigations: [
        `
        AWS CLI and API -
        - Use the describe-reserved-db-instances-offerings to list the Reserved DB Instance offerings available for purchase.
        - Use the purchase-reserved-db-instances-offering command to purchase RIs for an account.
        - Use the describe-reserved-db-instances command to list the existing RIs for an account.
        `,
      ],
      references: ['https://aws.amazon.com/rds/reserved-instances/'],
    },
    aws_rds_delete_protection: {
      title: 'Deletion Protection For RDS Instances',
      description: `Deletion protection prevents any existing or new provisioned databases clusters from being terminated by a root or an IAM user, using the AWS Management Console, AWS CLI, or AWS API, unless the feature is explicitly disabled. With Deletion Protection safety feature enabled, you have the certainty that your database clusters can't be accidentally deleted and make sure that your data remains safe.`,
      serviceName: 'AmazonRDS',
      recommendations: [
        `
        Ensure that all your provisioned Amazon Aurora database clusters are protected from accidental deletion by having the Deletion Protection feature enabled at the Aurora cluster level.
        `,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
            terraform {
                required_providers {
                    aws = {
                        source  = "hashicorp/aws"
                        version = "~> 3.27"
                    }
                }

                required_version = ">= 0.14.9"
            }

            provider "aws" {
                profile = "default"
                region  = "us-east-1"
            }

            resource "aws_rds_cluster_instance" "rds-cluster-instances" {
                count              = 2
                identifier         = "cc-aurora-mysql-cluster-xxx"
                cluster_identifier = aws_rds_cluster.rds-cluster.id
                instance_class     = "db.t2.small"
                engine             = aws_rds_cluster.rds-cluster.engine
                engine_version     = aws_rds_cluster.rds-cluster.engine_version
            }

            resource "aws_rds_cluster" "rds-cluster" {
                cluster_identifier      = "cc-aurora-mysql-cluster"
                engine                  = "aurora-mysql"
                engine_version          = "5.7.mysql_aurora.2.10.2"
                availability_zones      = ["us-east-1a", "us-east-1b"]
                database_name           = "auroradb"
                master_username         = "aurorausr"
                master_password         = "aurorapasswd"

                # Enable Deletion Protection For Aurora Cluster
                deletion_protection = true
            }
\`\`\`
`,
        `AWS CLI command to enable Deletion Protection for Aurora Cluster:
\`\`\`
        aws rds modify-db-cluster
        --region {{region}}
        --db-cluster-identifier cc-aurora-mysql-cluster
        --deletion-protection
        --apply-immediately
\`\`\`
`,
      ],
      references: ['https://aws.amazon.com/about-aws/whats-new/2018/09/amazon-rds-now-provides-database-deletion-protection/'],
    },
    aws_rds_storage_autoscaling: {
      title: 'Enable Instance Storage AutoScaling for RDS Instances',
      description:
        'With the Storage AutoScaling feature enabled, when Amazon RDS detects that your database instance is running out of disk space, it automatically scales up your instance storage. For example, you can use this feature for a new mobile application that users are adopting rapidly. In this case, a rapidly increasing workload might exceed the available database storage. To avoid having to manually scale up database storage, enable Amazon RDS Storage AutoScaling.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `Ensure that the Storage AutoScaling feature is enabled for your Amazon RDS database instances in order to provide dynamic scaling support for the database's storage based on your RDS application needs. Enabling Storage AutoScaling will allow the database instance storage to increase once the configured threshold is exceeded.`,
      ],
      mitigations: [
        `
        **Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                source  = "hashicorp/aws"
                version = "~> 3.27"
                }
            }

            required_version = ">= 0.14.9"
            }

            provider "aws" {
            profile = "default"
            region  = "us-east-1"
            }

            resource "aws_db_instance" "rds-database-instance" {
            allocated_storage     = 20
            engine                = "mysql"
            engine_version        = "5.7"
            instance_class        = "db.t2.micro"
            name                  = "mysqldb"
            username              = "ccmysqluser01"
            password              = "ccmysqluserpwd"
            parameter_group_name  = "default.mysql5.7"

            # Enable and Configure RDS Storage AutoScaling
            max_allocated_storage = 150

            apply_immediately = true
            }
\`\`\`
`,
        `
        AWS CLI command to enable Storage AutoScaling for RDS instance:
        \`\`\`
        aws rds modify-db-instance --region {{region}} --db-instance-identifier {{recommendation.instance_id}} --max-allocated-storage 150 --apply-immediately
        \`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_PIOPS.StorageTypes.html'],
    },
    aws_rds_auto_minor_upgrade: {
      title: 'Auto Minor Version Upgrade For RDS',
      description:
        'Amazon RDS will occasionally deprecate minor engine versions and provide new ones for upgrade. When the last version number within the release is replaced (e.g. 5.6.26 to 5.6.27), the version changed is considered minor. With the Auto Minor Version Upgrade feature enabled, the version upgrades will occur automatically during the specified maintenance window and your database instances will get the new features, the bug fixes, and the security patches for their database engines.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `
        Ensure that your Amazon RDS database instances have the Auto Minor Version Upgrade flag enabled in order to receive automatically minor engine upgrades during the specified maintenance window. Each version upgrade is available only after is tested and approved by AWS.
        `,
      ],
      mitigations: [
        `
        **Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                    source  = "hashicorp/aws"
                    version = "~> 3.27"
                }
            }

            required_version = ">= 0.14.9"
        }

        provider "aws" {
            profile = "default"
            region  = "us-east-1"
        }

        resource "aws_db_instance" "rds-database-instance" {
            allocated_storage     = 20
            engine                = "mysql"
            engine_version        = "5.7"
            instance_class        = "db.t2.small"
            name                  = "mysqldb"
            username              = "ccmysqluser01"
            password              = "ccmysqluserpwd"
            parameter_group_name  = "default.mysql5.7"

            # Enable Auto Minor Version Upgrade for Database Instances
            auto_minor_version_upgrade = true

            apply_immediately = true
        }
\`\`\`
`,

        `
        AWS CLI command to enable Auto Minor Version Upgrade for RDS instance:
        \`\`\`
        aws rds modify-db-instance
        --region {{region}}
        --db-instance-identifier {{recommendation.instance_id}}
        --auto-minor-version-upgrade
        --apply-immediately
        \`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_UpgradeDBInstance.Upgrading.html'],
    },
    aws_rds_performance_insights: {
      title: 'Performance Insights Should Be Enabled for RDS Instances',
      description:
        'The Performance Insights feature provides you instant visibility into the nature of the workloads on your Amazon RDS databases and helps you find the cause of any performance issue found on your databases.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `
        Ensure that your Amazon RDS MySQL and PostgreSQL database instances have the Performance Insights feature enabled in order to allow you to obtain a better overview of your databases performance as well as help you to identify potential performance issues. Performance Insights is a performance monitoring tool that helps you to evaluate the load on your MySQL/PostgreSQL databases and determine when and where to take action. The feature allows you to detect performance bottlenecks with an easy-to-understand dashboard that visualizes database load in real time. For example, with Performance Insights feature enabled, when the load of your database is high, you can easily determine the type of bottleneck such as high CPU consumption, lock waits or I/O latency, and see which SQL queries are creating the bottleneck. Performance Insights is currently available for the following database engines: Amazon Aurora (MySQL and PostgreSQL-compatible editions), RDS MySQL, and RDS PostgreSQL.
        `,
      ],
      mitigations: [
        `
        **Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                    source  = "hashicorp/aws"
                    version = "~> 3.27"
                }
            }

            required_version = ">= 0.14.9"
        }

        provider "aws" {
            profile = "default"
            region  = "us-east-1"
        }

        resource "aws_db_instance" "rds-database-instance" {
            allocated_storage     = 20
            engine                = "mysql"
            engine_version        = "5.7"
            instance_class        = "db.m4.large"
            name                  = "mysqldb"
            username              = "ccmysqluser01"
            password              = "ccmysqluserpwd"
            parameter_group_name  = "default.mysql5.7"

            # Enable and Configure the Performance Insights Feature
            performance_insights_enabled = true
            performance_insights_retention_period = 7
            performance_insights_kms_key_id = "arn:aws:kms:us-east-1:123456789012:key/abcdabcd-1234-1234-1234-abcdabcdabcd"

            apply_immediately = true
        }
\`\`\`
`,
        `AWS CLI command to enable Performance Insights for RDS instance:
\`\`\`
        aws rds modify-db-instance
        --region {{region}}
        --db-instance-identifier {{recommendation.instance_id}}
        --enable-performance-insights
        --performance-insights-retention-period 7
        --performance-insights-kms-key-id arn:aws:kms:us-east-1:123456789012:key/abcdabcd-1234-1234-1234-abcdabcdabcd
        --apply-immediately
        \`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_PerfInsights.html'],
    },
    aws_rds_backup_enabled: {
      title: 'Automated Backups Enabled for RDS Instances',
      description:
        'Creating point-in-time database instance snapshots periodically will allow you to handle efficiently your data restoration process in the event of a user error on the source database or to save data before making a major change to the instance database such as changing the structure of a table.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `Ensure that your Amazon RDS database instances have automated backups enabled for point-in-time recovery. To back up your database instances, Amazon RDS takes automatically a full daily snapshot of your data (with transactions logs) during the specified backup window and keeps the backups for a specified period of time (known as retention period).`,
      ],
      mitigations: [
        `
        **Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                    source  = "hashicorp/aws"
                    version = "~> 3.27"
                }
            }

            required_version = ">= 0.14.9"
        }

        provider "aws" {
            profile = "default"
            region  = "us-east-1"
        }

        resource "aws_db_instance" "rds-database-instance" {
            allocated_storage     = 20
            engine                = "mysql"
            engine_version        = "5.7"
            instance_class        = "db.t2.micro"
            name                  = "mysqldb"
            username              = "ccmysqluser01"
            password              = "ccmysqluserpwd"
            parameter_group_name  = "default.mysql5.7"

            # Enable and Configure Automated Backups
            backup_retention_period = 7

            apply_immediately = true
        }
\`\`\`
`,
        `AWS CLI command to enable Automated Backups for RDS instance:
\`\`\`
        aws rds modify-db-instance
        --region {{region}}
        --db-instance-identifier {{recommendation.instance_id}}
        --backup-retention-period 7
        --apply-immediately
\`\`\`
`,
      ],
      references: ['https://aws.amazon.com/rds/features/backup/'],
    },
    aws_rds_backupservice_enabled: {
      title: 'AWS Backup Service in Use for Amazon RDS',
      description:
        'With Amazon Backup, you can centrally configure backup policies and rules, and monitor backup activity for AWS RDS database instances. The Backup service automates and consolidates backup tasks previously performed service-by-service, removing the need to create custom scripts such as Lambda functions and manual processes.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `
        Ensure that Amazon Backup is integrated with Amazon Relational Database Service (RDS) in order to manage RDS database instance snapshots and improve the reliability of your backup strategy. Amazon Backup is a fully managed service that creates, restores and deletes backups on your behalf.
        `,
      ],
      mitigations: [
        `
        Create Backup Plan for RDS Database Instance:
        \`\`\`
      aws backup create-backup-plan --region {{region}} --backup-plan file://daily-35day-retention.json      
        \`\`\`
        `,
      ],
      references: ['https://aws.amazon.com/rds/features/backup/'],
    },
    aws_rds_copy_tags_to_snapshots: {
      title: 'Enable Copy Tags to Snapshots for RDS Instances',
      description:
        'Copying your Amazon Aurora database cluster tags to any automated or manual snapshots taken from your database clusters allows you to easily set metadata (including access policies) on your snapshots in order to match the parent clusters.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `Ensure that your Amazon Aurora database clusters make use of Copy Tags to Snapshots feature in order to allow tags set on your Aurora database clusters to be automatically copied to any automated or manual snapshots that are created from these clusters. Once the feature is enabled, tags can be copied to all future copies of an Amazon Aurora snapshots, including cross-region snapshots.`,
      ],
      mitigations: [
        `
        **Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                    source  = "hashicorp/aws"
                    version = "~> 3.27"
                }
            }

            required_version = ">= 0.14.9"
        }

        provider "aws" {
            profile = "default"
            region  = "us-east-1"
        }

        resource "aws_rds_cluster_instance" "rds-cluster-instances" {
            count              = 2
            identifier         = "cc-aurora-mysql-cluster-1"
            cluster_identifier = aws_rds_cluster.rds-cluster.id
            instance_class     = "db.t2.small"
            engine             = aws_rds_cluster.rds-cluster.engine
            engine_version     = aws_rds_cluster.rds-cluster.engine_version
        }

        resource "aws_rds_cluster" "rds-cluster" {
            cluster_identifier      = "cc-aurora-mysql-cluster"
            engine                  = "aurora-mysql"
            engine_version          = "5.7.mysql_aurora.2.10.2"
            availability_zones      = ["us-east-1a", "us-east-1b"]
            database_name           = "auroradb"
            master_username         = "aurorausr"
            master_password         = "aurorapasswd"

            # Enable Copy Tags to Snapshots For Aurora Cluster
            copy_tags_to_snapshot = true
        }
\`\`\`
`,

        `
        AWS CLI command to enable Copy Tags to Snapshots for RDS Cluster:
        \`\`\`
        aws rds modify-db-cluster
        --region {{region}}
        --db-cluster-identifier cc-aurora-mysql-cluster
        --copy-tags-to-snapshot
        --apply-immediately
        \`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_Tagging.html'],
    },
    aws_s3_versioning: {
      title: 'Bucket Versioning should be enabled for S3 Buckets',
      description:
        'Versioning-enabled Amazon S3 buckets will allow you to preserve, retrieve, and restore every version of an S3 object. S3 versioning can be used for data protection and retention scenarios such as recovering objects that have been accidentally/intentionally deleted or overwritten by AWS users or applications and archiving previous versions of objects to Amazon S3 Glacier for long-term low-cost storage. With S3 versioning, you can easily recover from both unintended user actions and application failures.',
      serviceName: 'AmazonS3',
      recommendations: [
        `Ensure that S3 object versioning is enabled for your Amazon S3 buckets in order to preserve and recover overwritten and deleted S3 objects as an extra layer of data protection and/or data retention.`,
      ],
      mitigations: [
        `
        **Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                source  = "hashicorp/aws"
                version = "~> 3.27"
                }
            }

            required_version = ">= 0.14.9"
            }

            provider "aws" {
            profile = "default"
            region  = "us-east-1"
            }

            resource "aws_s3_bucket" "protected-bucket" {
            bucket = "cc-prod-web-data"
            versioning {
                enabled = true
            }
            }
\`\`\`
`,
        `
        AWS CLI command to enable Versioning for S3 bucket:
        \`\`\`
        aws s3api put-bucket-versioning --bucket cc-prod-web-data --versioning-configuration Status=Enabled
        \`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/AmazonS3/latest/userguide/Versioning.html'],
    },
    aws_s3_lifecycle: {
      title: 'Lifecycle Configuration Enabled for S3 Buckets',
      description:
        'With an S3 lifecycle configuration, you can enable Amazon S3 to downgrade the storage class for your objects, archive or delete S3 objects during their lifecycle. For example, you can define S3 lifecycle configuration rules to achieve compliance (with the law, with your organization standards or business requirements) by automatically transitioning your objects to S3 Standard-Infrequent Access (S3 Standard-IA) storage class one month after creation, or archive S3 objects with Amazon S3 Glacier using Glacier or Glacier Deep Archive storage class one year after creation. You can also implement lifecycle configuration rules to expire (delete) objects based on your retention requirements or clean up incomplete multipart uploads in order to optimize your Amazon S3 costs.',
      serviceName: 'AmazonS3',
      recommendations: [
        `Ensure that your Amazon S3 buckets are using lifecycle configurations for security and cost optimization purposes. An S3 lifecycle configuration is a set of one or more rules, where each rule defines an action (transition or expiration action) for Amazon S3 to apply to a group of objects. A lifecycle configuration is used to manage Amazon S3 data during its lifetime.`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                source = "hashicorp/aws"
                version = "~> 3.27"
                }
            }

            required_version = ">= 0.14.9"
            }

            provider "aws" {
            profile = "default"
            region = "us-east-1"
            }

            resource "aws_s3_bucket" "data-bucket" {
            bucket = "cc-prod-web-data"
            acl = "private"

            lifecycle_rule {
                id = "cc-transition-access-log-data"
                enabled = true

                prefix = "log/"

                tags = {
                rule = "log"
                autoclean = "true"
                }

                transition {
                days = 30
                storage_class = "STANDARD_IA"
                }

                transition {
                days = 60
                storage_class = "GLACIER"
                }

                expiration {
                days = 365
                }
            }

            }
\`\`\`
`,
        ` AWS CLI command to enable Lifecycle Configuration for S3 bucket:
\`\`\`
            aws s3api put-bucket-lifecycle-configuration
            --bucket cc-prod-web-data
            --lifecycle-configuration '{
            "Rules": [
                {
                "ID": "cc-transition-access-log-data",
                "Status": "Enabled",
                "Filter": {},
                "Expiration": {
                    "Days": 365
                },
                "Transitions": [
                    {
                    "Days": 30,
                    "StorageClass": "STANDARD_IA"
                    },
                    {
                    "Days": 60,
                    "StorageClass": "GLACIER"
                    }
                ]
                }
            ]
            }'                   
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lifecycle-mgmt.html'],
    },
    aws_elasticache_auto_minor_upgrade: {
      title: 'Minor Version Upgrade Enabled for ElastiCache Clusters',
      description:
        'Amazon ElasticCache will occasionally deprecate minor engine versions and provide new ones for upgrade. When the last version number within the release is replaced (e.g. 5.6.26 to 5.6.27), the version changed is considered minor. With the Auto Minor Version Upgrade feature enabled, the version upgrades will occur automatically during the specified maintenance window and your database instances will get the new features, the bug fixes, and the security patches for their database engines.',
      serviceName: 'AmazonElastiCache',
      recommendations: [
        `
        Ensure that your Amazon ElasticCache cluster instances have the Auto Minor Version Upgrade flag enabled in order to receive automatically minor engine upgrades during the specified maintenance window. Each version upgrade is available only after is tested and approved by AWS.
        `,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
\`\`\`
        `,
        `
        AWS CLI command to enable Auto Minor Version Upgrade for ElastiCache cluster:
        \`\`\`
        aws elasticache modify-cache-cluster --cache-cluster-id {{recommendation.cluster_id}} --preferred-maintenance-window sun:23:00-mon:02:00
        \`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/VersionManagement.html'],
    },
    // --- AWS Fargate Configuration ---
    aws_fargate_service_min_healthy_percent_low: {
      title: 'Fargate Service Minimum Healthy Percent Too Low',
      description: 'The Fargate service minimum healthy percent is below 50%, which may impact availability during deployments.',
      serviceName: 'AWSFargate',
      recommendations: [
        'Set minimum healthy percent to at least 50% for services with multiple tasks to maintain availability during rolling deployments.',
      ],
      mitigations: ['Update the deployment configuration in the ECS service definition to set `minimumHealthyPercent` to at least 50%.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service_definition_parameters.html'],
    },
    aws_fargate_service_min_healthy_percent_too_low_for_single_task: {
      title: 'Single-Task Fargate Service Minimum Healthy Percent Below 100%',
      description: 'For a single-task service, minimum healthy percent less than 100% can cause downtime during updates.',
      serviceName: 'AWSFargate',
      recommendations: ['Set minimum healthy percent to 100% for single-task services to avoid downtime during deployments.'],
      mitigations: ['Update the deployment configuration to set `minimumHealthyPercent` to 100% for single-task services.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service_definition_parameters.html'],
    },
    aws_fargate_service_max_percent_non_standard: {
      title: 'Fargate Service Maximum Percent Non-Standard',
      description: 'The maximum percent deployment configuration is not set to the standard 200%.',
      serviceName: 'AWSFargate',
      recommendations: ['Review if the non-standard maximum percent is intentional. Standard is 200% to allow rolling deployments.'],
      mitigations: ['Update the deployment configuration to set `maximumPercent` to 200% unless a different value is specifically required.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service_definition_parameters.html'],
    },
    aws_fargate_service_health_check_grace_period_zero: {
      title: 'Fargate Service Health Check Grace Period Is Zero',
      description: 'The health check grace period is set to 0, which may cause tasks to be terminated before they finish starting up.',
      serviceName: 'AWSFargate',
      recommendations: ['Set a health check grace period that gives tasks enough time to start up and pass health checks.'],
      mitigations: ['Update the ECS service to set `healthCheckGracePeriodSeconds` to an appropriate value (e.g., 60-120 seconds).'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service_definition_parameters.html'],
    },
    aws_fargate_service_exec_enabled: {
      title: 'Fargate Service ECS Exec Is Enabled',
      description:
        'ECS Exec is enabled on this Fargate service. This feature allows interactive shell access to running containers and should be disabled in production.',
      serviceName: 'AWSFargate',
      recommendations: ['Disable ECS Exec on production services to reduce the attack surface. Only enable it for debugging when needed.'],
      mitigations: ['Update the ECS service to set `enableExecuteCommand` to false.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs-exec.html'],
    },
    aws_fargate_task_definition_cpu_undefined: {
      title: 'Fargate Task Definition CPU Not Defined',
      description: 'The Fargate task definition does not have CPU units defined, which can lead to unpredictable resource allocation.',
      serviceName: 'AWSFargate',
      recommendations: ['Define CPU units in the task definition to ensure predictable resource allocation.'],
      mitigations: ['Update the task definition to specify `cpu` at the task level.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html'],
    },
    aws_fargate_task_definition_memory_undefined: {
      title: 'Fargate Task Definition Memory Not Defined',
      description: 'The Fargate task definition does not have memory defined, which can lead to unpredictable resource allocation.',
      serviceName: 'AWSFargate',
      recommendations: ['Define memory in the task definition to ensure predictable resource allocation.'],
      mitigations: ['Update the task definition to specify `memory` at the task level.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html'],
    },
    aws_fargate_task_definition_missing_task_role: {
      title: 'Fargate Task Definition Missing Task Role',
      description:
        'The task definition does not have an IAM task role assigned. Without a task role, containers cannot securely access AWS services.',
      serviceName: 'AWSFargate',
      recommendations: ['Assign an IAM task role to the task definition following the principle of least privilege.'],
      mitigations: ['Update the task definition to include a `taskRoleArn` with appropriate permissions.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-iam-roles.html'],
    },
    aws_fargate_task_definition_image_latest_tag: {
      title: 'Fargate Task Definition Using Latest Image Tag',
      description: 'The container image uses the :latest tag, which makes deployments non-deterministic and harder to rollback.',
      serviceName: 'AWSFargate',
      recommendations: ['Use specific image tags or digests instead of :latest to ensure reproducible deployments.'],
      mitigations: ['Update the container definition to use a specific image tag (e.g., v1.2.3 or a SHA digest).'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html'],
    },
    aws_fargate_task_definition_logging_not_configured: {
      title: 'Fargate Task Definition Logging Not Configured',
      description: 'The container does not have logging configured, which makes it difficult to troubleshoot issues.',
      serviceName: 'AWSFargate',
      recommendations: ['Configure a log driver (e.g., awslogs) for all containers in the task definition.'],
      mitigations: ['Add a `logConfiguration` section to the container definition with an appropriate log driver.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/using_awslogs.html'],
    },
    aws_fargate_task_definition_health_check_not_configured: {
      title: 'Fargate Task Definition Health Check Not Configured',
      description: 'The container does not have a health check configured, which means unhealthy containers may not be detected.',
      serviceName: 'AWSFargate',
      recommendations: ['Configure a health check for containers to enable automatic detection and replacement of unhealthy tasks.'],
      mitigations: ['Add a `healthCheck` section to the container definition with an appropriate command and interval.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html#container_definition_healthcheck'],
    },
    // --- AWS Redshift Configuration ---
    aws_redshift_audit_logging: {
      title: 'Redshift Audit Logging Should Be Enabled',
      description: 'Amazon Redshift audit logging provides information about connections and user activities in your cluster.',
      serviceName: 'AmazonRedshift',
      recommendations: ['Enable audit logging for your Redshift cluster to track database activity for security and compliance purposes.'],
      mitigations: [
        'Enable audit logging via the Redshift console or CLI: `aws redshift modify-cluster --cluster-identifier {{recommendation.cluster_id}} --logging-properties BucketName=my-bucket`',
      ],
      references: ['https://docs.aws.amazon.com/redshift/latest/mgmt/db-auditing.html'],
    },
    aws_redshift_snapshot_retention: {
      title: 'Redshift Snapshot Retention Period Should Be Configured',
      description: 'The Redshift cluster snapshot retention period should be set to ensure backups are maintained for an adequate period.',
      serviceName: 'AmazonRedshift',
      recommendations: ['Configure an appropriate snapshot retention period for your Redshift cluster.'],
      mitigations: [
        'Modify the cluster snapshot retention: `aws redshift modify-cluster --cluster-identifier {{recommendation.cluster_id}} --automated-snapshot-retention-period 7`',
      ],
      references: ['https://docs.aws.amazon.com/redshift/latest/mgmt/working-with-snapshots.html'],
    },
    // --- AWS GuardDuty Configuration ---
    aws_guardduty_enabled: {
      title: 'GuardDuty Should Be Enabled',
      description: 'Amazon GuardDuty is a threat detection service that continuously monitors for malicious activity and unauthorized behavior.',
      serviceName: 'AmazonGuardDuty',
      recommendations: ['Enable GuardDuty in all regions to detect threats across your AWS environment.'],
      mitigations: [
        `
**Enable GuardDuty:**
\`\`\`
aws guardduty create-detector --enable
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/guardduty/latest/ug/guardduty_settingup.html'],
    },
    aws_guardduty_detector_active: {
      title: 'GuardDuty Detector Should Be Active',
      description: 'The GuardDuty detector exists but is not in an active state, meaning threats are not being monitored.',
      serviceName: 'AmazonGuardDuty',
      recommendations: ['Ensure the GuardDuty detector is active to continuously monitor for threats.'],
      mitigations: [
        `
**Activate the detector:**
\`\`\`
aws guardduty update-detector --detector-id <id> --enable
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/guardduty/latest/ug/guardduty_settingup.html'],
    },
    aws_guardduty_s3_logs_enabled: {
      title: 'GuardDuty S3 Protection Should Be Enabled',
      description: 'GuardDuty S3 Protection monitors S3 data access events to detect suspicious activity.',
      serviceName: 'AmazonGuardDuty',
      recommendations: ['Enable S3 Protection in GuardDuty to monitor S3 bucket access for threats.'],
      mitigations: ['Update the detector to enable S3 logs data source.'],
      references: ['https://docs.aws.amazon.com/guardduty/latest/ug/s3-protection.html'],
    },
    aws_guardduty_k8s_audit_logs_enabled: {
      title: 'GuardDuty Kubernetes Audit Logs Should Be Enabled',
      description: 'GuardDuty EKS Protection monitors Kubernetes audit logs for suspicious activity in EKS clusters.',
      serviceName: 'AmazonGuardDuty',
      recommendations: ['Enable Kubernetes audit log monitoring in GuardDuty for EKS cluster threat detection.'],
      mitigations: ['Update the detector to enable Kubernetes data source.'],
      references: ['https://docs.aws.amazon.com/guardduty/latest/ug/kubernetes-protection.html'],
    },
    // --- AWS Elasticsearch Configuration ---
    aws_es_dedicated_master: {
      title: 'Elasticsearch Dedicated Master Nodes Should Be Enabled',
      description: 'Dedicated master nodes improve cluster stability by offloading cluster management tasks from data nodes.',
      serviceName: 'AmazonES',
      recommendations: ['Enable dedicated master nodes for production Elasticsearch domains to improve stability.'],
      mitigations: ['Update the domain configuration to enable dedicated master nodes with at least 3 instances.'],
      references: ['https://docs.aws.amazon.com/opensearch-service/latest/developerguide/managedomains-dedicatedmasternodes.html'],
    },
    aws_es_audit_logs_enabled: {
      title: 'Elasticsearch Audit Logs Should Be Enabled',
      description: 'Audit logs capture user activity in your Elasticsearch domain for security monitoring and compliance.',
      serviceName: 'AmazonES',
      recommendations: ['Enable audit logs for your Elasticsearch domain to track user activity.'],
      mitigations: ['Update the domain log publishing options to enable audit logs.'],
      references: ['https://docs.aws.amazon.com/opensearch-service/latest/developerguide/audit-logs.html'],
    },
    aws_es_slow_logs_enabled: {
      title: 'Elasticsearch Slow Logs Should Be Enabled',
      description: 'Slow logs help identify performance bottlenecks in your Elasticsearch domain.',
      serviceName: 'AmazonES',
      recommendations: ['Enable slow logs to identify and troubleshoot query and indexing performance issues.'],
      mitigations: ['Update the domain log publishing options to enable slow logs.'],
      references: ['https://docs.aws.amazon.com/opensearch-service/latest/developerguide/createdomain-configure-slow-logs.html'],
    },
    // --- AWS Direct Connect ---
    aws_directconnect_connection_down: {
      title: 'Direct Connect Connection Is Down',
      description: 'The Direct Connect connection is not in an available state, which may impact network connectivity.',
      serviceName: 'AWSDirectConnect',
      recommendations: ['Investigate and resolve the Direct Connect connection issue to restore connectivity.'],
      mitigations: ['Check the AWS Direct Connect console for connection status and contact AWS Support if needed.'],
      references: ['https://docs.aws.amazon.com/directconnect/latest/UserGuide/WorkingWithConnections.html'],
    },
    aws_directconnect_no_redundancy: {
      title: 'Direct Connect Has No Redundancy',
      description: 'The Direct Connect setup does not have redundant connections, creating a single point of failure.',
      serviceName: 'AWSDirectConnect',
      recommendations: ['Set up redundant Direct Connect connections for high availability.'],
      mitigations: ['Create a second Direct Connect connection at a different location for redundancy.'],
      references: ['https://docs.aws.amazon.com/directconnect/latest/UserGuide/getting_started.html'],
    },
    aws_directconnect_no_virtual_interfaces: {
      title: 'Direct Connect Has No Virtual Interfaces',
      description: 'The Direct Connect connection has no virtual interfaces configured.',
      serviceName: 'AWSDirectConnect',
      recommendations: ['Configure virtual interfaces on the Direct Connect connection to establish connectivity.'],
      mitigations: ['Create a virtual interface on the Direct Connect connection.'],
      references: ['https://docs.aws.amazon.com/directconnect/latest/UserGuide/WorkingWithVirtualInterfaces.html'],
    },
    aws_directconnect_lag_below_minimum: {
      title: 'Direct Connect LAG Below Minimum Connections',
      description: 'The Link Aggregation Group has fewer active connections than the minimum threshold.',
      serviceName: 'AWSDirectConnect',
      recommendations: ['Ensure the LAG has sufficient active connections for required bandwidth.'],
      mitigations: ['Add more connections to the LAG or reduce the minimum links threshold.'],
      references: ['https://docs.aws.amazon.com/directconnect/latest/UserGuide/lags.html'],
    },
    aws_directconnect_lag_down: {
      title: 'Direct Connect LAG Is Down',
      description: 'The Direct Connect Link Aggregation Group is not operational.',
      serviceName: 'AWSDirectConnect',
      recommendations: ['Investigate and resolve the LAG issue to restore connectivity.'],
      mitigations: ['Check the LAG status and ensure member connections are active.'],
      references: ['https://docs.aws.amazon.com/directconnect/latest/UserGuide/lags.html'],
    },
    aws_directconnect_vif_down: {
      title: 'Direct Connect Virtual Interface Is Down',
      description: 'The Direct Connect virtual interface is not in an available state.',
      serviceName: 'AWSDirectConnect',
      recommendations: ['Investigate and resolve the virtual interface issue.'],
      mitigations: ['Check the VIF status and ensure BGP session is established.'],
      references: ['https://docs.aws.amazon.com/directconnect/latest/UserGuide/WorkingWithVirtualInterfaces.html'],
    },
    aws_directconnect_bgp_peer_down: {
      title: 'Direct Connect BGP Peer Is Down',
      description: 'The BGP peer on the Direct Connect virtual interface is not established.',
      serviceName: 'AWSDirectConnect',
      recommendations: ['Investigate the BGP peer issue and restore the session.'],
      mitigations: ['Verify BGP configuration on both sides and check for network issues.'],
      references: ['https://docs.aws.amazon.com/directconnect/latest/UserGuide/WorkingWithVirtualInterfaces.html'],
    },
    // --- AWS ELB Configuration ---
    aws_elb_cross_zone_balancing: {
      title: 'ELB Cross-Zone Load Balancing Should Be Enabled',
      description: 'Cross-zone load balancing distributes traffic evenly across all registered instances in all enabled Availability Zones.',
      serviceName: 'AmazonELB',
      recommendations: ['Enable cross-zone load balancing to ensure even traffic distribution across AZs.'],
      mitigations: [
        'Enable cross-zone load balancing: `aws elb modify-load-balancer-attributes --load-balancer-name {{resource_name}} --load-balancer-attributes "{"CrossZoneLoadBalancing":{"Enabled":true}}"`',
      ],
      references: ['https://docs.aws.amazon.com/elasticloadbalancing/latest/classic/enable-disable-crosszone-lb.html'],
    },
    aws_elb_connection_draining: {
      title: 'ELB Connection Draining Should Be Enabled',
      description: 'Connection draining ensures that the load balancer completes in-flight requests before deregistering instances.',
      serviceName: 'AmazonELB',
      recommendations: ['Enable connection draining to allow in-flight requests to complete during instance deregistration.'],
      mitigations: ['Enable connection draining with an appropriate timeout value.'],
      references: ['https://docs.aws.amazon.com/elasticloadbalancing/latest/classic/config-conn-drain.html'],
    },
    aws_elb_access_logs: {
      title: 'ELB Access Logs Should Be Enabled',
      description: 'Access logs capture detailed information about requests sent to your load balancer for analysis and troubleshooting.',
      serviceName: 'AmazonELB',
      recommendations: ['Enable access logging for your load balancer to capture request details.'],
      mitigations: ['Enable access logs and configure an S3 bucket to store them.'],
      references: ['https://docs.aws.amazon.com/elasticloadbalancing/latest/classic/access-log-collection.html'],
    },
    aws_elb_no_listeners: {
      title: 'ELB Has No Listeners Configured',
      description: 'The load balancer has no listeners configured, meaning it cannot route any traffic.',
      serviceName: 'AmazonELB',
      recommendations: ['Configure at least one listener on the load balancer.'],
      mitigations: ['Add a listener with appropriate protocol and port configuration.'],
      references: ['https://docs.aws.amazon.com/elasticloadbalancing/latest/classic/elb-listener-config.html'],
    },
    // --- AWS EC2 Additional Configuration ---
    aws_ec2_instance_termination_protection_disabled: {
      title: 'EC2 Instance Termination Protection Disabled',
      description: 'The EC2 instance does not have termination protection enabled, making it vulnerable to accidental termination.',
      serviceName: 'AmazonEC2',
      recommendations: ['Enable termination protection for important EC2 instances.'],
      mitigations: [
        `
\`\`\`
aws ec2 modify-instance-attribute --instance-id i-xxx --disable-api-termination
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Using_ChangingDisableAPITermination.html'],
    },
    aws_ec2_instance_terminates_on_os_shutdown: {
      title: 'EC2 Instance Terminates on OS Shutdown',
      description: 'The instance is configured to terminate when an OS-level shutdown is performed instead of stopping.',
      serviceName: 'AmazonEC2',
      recommendations: ['Set the instance shutdown behavior to "stop" instead of "terminate".'],
      mitigations: [
        `
\`\`\`
aws ec2 modify-instance-attribute --instance-id i-xxx --instance-initiated-shutdown-behavior stop
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/terminating-instances.html#Using_ChangingInstanceInitiatedShutdownBehavior'],
    },
    aws_ec2_detailed_monitoring_disabled: {
      title: 'EC2 Detailed Monitoring Disabled',
      description: 'Detailed monitoring provides 1-minute CloudWatch metrics instead of the default 5-minute intervals.',
      serviceName: 'AmazonEC2',
      recommendations: ['Enable detailed monitoring for better visibility into instance performance.'],
      mitigations: [
        `
\`\`\`
aws ec2 monitor-instances --instance-ids i-xxx
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-cloudwatch-new.html'],
    },
    // --- AWS Route53 Configuration ---
    aws_route53_query_logging_disabled: {
      title: 'Route 53 Query Logging Disabled',
      description: 'DNS query logging is not enabled for this hosted zone.',
      serviceName: 'AmazonRoute53',
      recommendations: ['Enable query logging to monitor DNS queries for troubleshooting and security analysis.'],
      mitigations: ['Create a query logging configuration for the hosted zone.'],
      references: ['https://docs.aws.amazon.com/Route53/latest/DeveloperGuide/query-logs.html'],
    },
    aws_route53_dnssec_disabled: {
      title: 'Route 53 DNSSEC Not Enabled',
      description: 'DNSSEC signing is not enabled for this hosted zone, leaving it vulnerable to DNS spoofing attacks.',
      serviceName: 'AmazonRoute53',
      recommendations: ['Enable DNSSEC signing to protect against DNS spoofing and cache poisoning attacks.'],
      mitigations: ['Enable DNSSEC signing in the Route 53 console or CLI.'],
      references: ['https://docs.aws.amazon.com/Route53/latest/DeveloperGuide/domain-configure-dnssec.html'],
    },
    aws_route53_empty_hosted_zone: {
      title: 'Route 53 Empty Hosted Zone',
      description: 'The hosted zone has no record sets beyond the default NS and SOA records.',
      serviceName: 'AmazonRoute53',
      recommendations: ['Review if this hosted zone is needed. Empty hosted zones incur costs.'],
      mitigations: ['Delete the hosted zone if it is no longer needed, or add appropriate DNS records.'],
      references: ['https://docs.aws.amazon.com/Route53/latest/DeveloperGuide/hosted-zones-working-with.html'],
    },
    aws_route53_unhealthy_health_check: {
      title: 'Route 53 Unhealthy Health Check',
      description: 'A Route 53 health check is reporting an unhealthy status.',
      serviceName: 'AmazonRoute53',
      recommendations: ['Investigate and resolve the unhealthy health check to ensure proper DNS failover.'],
      mitigations: ['Check the target endpoint health and update the health check configuration if needed.'],
      references: ['https://docs.aws.amazon.com/Route53/latest/DeveloperGuide/health-checks-types.html'],
    },
    aws_route53_health_check_no_alarm: {
      title: 'Route 53 Health Check Has No CloudWatch Alarm',
      description: 'The health check does not have a CloudWatch alarm configured for failure notifications.',
      serviceName: 'AmazonRoute53',
      recommendations: ['Create a CloudWatch alarm for the health check to receive notifications on failures.'],
      mitigations: ['Create a CloudWatch alarm monitoring the `HealthCheckStatus` metric.'],
      references: ['https://docs.aws.amazon.com/Route53/latest/DeveloperGuide/health-checks-monitor-view-status.html'],
    },
    // --- AWS Step Functions ---
    aws_stepfunctions_high_failure_rate: {
      title: 'Step Functions High Failure Rate',
      description: 'The Step Functions state machine has a high execution failure rate.',
      serviceName: 'AWSStepFunctions',
      recommendations: ['Investigate the root cause of failures and implement error handling.'],
      mitigations: ['Add Catch and Retry blocks to handle transient failures in your state machine definition.'],
      references: ['https://docs.aws.amazon.com/step-functions/latest/dg/concepts-error-handling.html'],
    },
    aws_stepfunctions_logging_disabled: {
      title: 'Step Functions Logging Disabled',
      description: 'Logging is disabled for this state machine, making it difficult to troubleshoot execution issues.',
      serviceName: 'AWSStepFunctions',
      recommendations: ['Enable CloudWatch Logs for the state machine to track execution details.'],
      mitigations: ['Update the state machine to include a logging configuration with a CloudWatch Logs log group.'],
      references: ['https://docs.aws.amazon.com/step-functions/latest/dg/cw-logs.html'],
    },
    aws_stepfunctions_logging_not_configured: {
      title: 'Step Functions Logging Not Configured',
      description: 'The state machine does not have a logging configuration defined.',
      serviceName: 'AWSStepFunctions',
      recommendations: ['Configure logging for the state machine.'],
      mitigations: ['Add a logging configuration with appropriate log level (ALL, ERROR, or FATAL).'],
      references: ['https://docs.aws.amazon.com/step-functions/latest/dg/cw-logs.html'],
    },
    aws_stepfunctions_xray_tracing_disabled: {
      title: 'Step Functions X-Ray Tracing Disabled',
      description: 'X-Ray tracing is not enabled for this state machine.',
      serviceName: 'AWSStepFunctions',
      recommendations: ['Enable X-Ray tracing to gain insights into execution performance and bottlenecks.'],
      mitigations: ['Update the state machine to enable X-Ray tracing.'],
      references: ['https://docs.aws.amazon.com/step-functions/latest/dg/concepts-xray-tracing.html'],
    },
    aws_stepfunctions_express_not_utilized: {
      title: 'Step Functions Express Workflow Not Utilized',
      description: 'The state machine could benefit from using Express Workflows for high-volume, short-duration executions.',
      serviceName: 'AWSStepFunctions',
      recommendations: ['Consider using Express Workflows for workloads with high execution rates and short durations.'],
      mitigations: ['Create a new Express state machine if the workload pattern is suitable.'],
      references: ['https://docs.aws.amazon.com/step-functions/latest/dg/concepts-standard-vs-express.html'],
    },
    aws_stepfunctions_large_definition: {
      title: 'Step Functions Large State Machine Definition',
      description: 'The state machine definition is unusually large, which may indicate it should be broken into smaller machines.',
      serviceName: 'AWSStepFunctions',
      recommendations: ['Consider breaking large state machines into smaller, composable state machines.'],
      mitigations: ['Refactor the state machine into nested workflows or separate state machines.'],
      references: ['https://docs.aws.amazon.com/step-functions/latest/dg/limits-overview.html'],
    },
    // --- AWS MSK Configuration ---
    aws_msk_enhanced_monitoring: {
      title: 'MSK Enhanced Monitoring Should Be Enabled',
      description: 'Enhanced monitoring provides additional broker-level metrics for better visibility into cluster performance.',
      serviceName: 'AmazonMSK',
      recommendations: ['Enable enhanced monitoring for your MSK cluster.'],
      mitigations: ['Update the cluster to use PER_BROKER or PER_TOPIC_PER_BROKER monitoring level.'],
      references: ['https://docs.aws.amazon.com/msk/latest/developerguide/monitoring.html'],
    },
    aws_msk_cloudwatch_logs: {
      title: 'MSK CloudWatch Logs Should Be Enabled',
      description: 'Broker logs should be sent to CloudWatch Logs for monitoring and troubleshooting.',
      serviceName: 'AmazonMSK',
      recommendations: ['Enable CloudWatch Logs for MSK broker logs.'],
      mitigations: ['Update the cluster logging configuration to enable CloudWatch Logs.'],
      references: ['https://docs.aws.amazon.com/msk/latest/developerguide/msk-logging.html'],
    },
    // --- AWS CloudFormation ---
    aws_cfn_drift_detection_check: {
      title: 'CloudFormation Drift Detection Check',
      description: 'CloudFormation stack drift should be checked regularly to detect manual changes to managed resources.',
      serviceName: 'AWSCloudFormation',
      recommendations: ['Run drift detection periodically to identify resources that have been modified outside of CloudFormation.'],
      mitigations: [
        `
**Run drift detection:**
\`\`\`
aws cloudformation detect-stack-drift --stack-name {{resource_name}}
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/using-cfn-stack-drift.html'],
    },
    aws_cfn_stack_drifted: {
      title: 'CloudFormation Stack Has Drifted',
      description: 'The CloudFormation stack has drifted from its template, meaning resources have been modified outside of CloudFormation.',
      serviceName: 'AWSCloudFormation',
      recommendations: ['Resolve drift by updating the template or reverting manual changes.'],
      mitigations: ['Update the stack to match the current template or import the drifted resources.'],
      references: ['https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/using-cfn-stack-drift.html'],
    },
    aws_cfn_termination_protection: {
      title: 'CloudFormation Termination Protection Should Be Enabled',
      description: 'Termination protection prevents the stack from being accidentally deleted.',
      serviceName: 'AWSCloudFormation',
      recommendations: ['Enable termination protection for important CloudFormation stacks.'],
      mitigations: [
        `
\`\`\`
aws cloudformation update-termination-protection --enable-termination-protection --stack-name {{resource_name}}
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/using-cfn-protect-stacks.html'],
    },
    aws_cfn_stack_policy: {
      title: 'CloudFormation Stack Policy Should Be Configured',
      description: 'A stack policy prevents unintentional updates to stack resources.',
      serviceName: 'AWSCloudFormation',
      recommendations: ['Set a stack policy to protect critical resources from unintentional updates.'],
      mitigations: [
        'Set a stack policy: `aws cloudformation set-stack-policy --stack-name {{resource_name}} --stack-policy-body file://policy.json`',
      ],
      references: ['https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/protect-stack-resources.html'],
    },
    // --- AWS CloudTrail ---
    aws_cloudtrail_multi_region: {
      title: 'CloudTrail Multi-Region Logging Should Be Enabled',
      description: 'CloudTrail should be configured to log events from all AWS regions.',
      serviceName: 'AWSCloudTrail',
      recommendations: ['Enable multi-region logging to capture API activity across all regions.'],
      mitigations: [
        `
**Update the trail to enable multi-region:**
\`\`\`
aws cloudtrail update-trail --name {{resource_name}} --is-multi-region-trail
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/awscloudtrail/latest/userguide/receive-cloudtrail-log-files-from-multiple-regions.html'],
    },
    aws_cloudtrail_log_validation: {
      title: 'CloudTrail Log File Validation Should Be Enabled',
      description: 'Log file validation ensures that CloudTrail log files have not been tampered with after delivery.',
      serviceName: 'AWSCloudTrail',
      recommendations: ['Enable log file validation to detect unauthorized modifications to log files.'],
      mitigations: [
        `
\`\`\`
aws cloudtrail update-trail --name {{resource_name}} --enable-log-file-validation
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/awscloudtrail/latest/userguide/cloudtrail-log-file-validation-intro.html'],
    },
    aws_cloudtrail_cloudwatch_integration: {
      title: 'CloudTrail Should Be Integrated with CloudWatch',
      description: 'Integrating CloudTrail with CloudWatch Logs enables real-time monitoring of API activity.',
      serviceName: 'AWSCloudTrail',
      recommendations: ['Configure CloudTrail to send logs to a CloudWatch Logs group for real-time monitoring and alerting.'],
      mitigations: ['Update the trail to specify a CloudWatch Logs log group and IAM role.'],
      references: ['https://docs.aws.amazon.com/awscloudtrail/latest/userguide/send-cloudtrail-events-to-cloudwatch-logs.html'],
    },
    aws_cloudtrail_logging_enabled: {
      title: 'CloudTrail Logging Should Be Enabled',
      description: 'CloudTrail logging is not enabled, meaning API calls are not being recorded.',
      serviceName: 'AWSCloudTrail',
      recommendations: ['Enable CloudTrail logging to record API calls for security and compliance.'],
      mitigations: [
        `
**Start logging:**
\`\`\`
aws cloudtrail start-logging --name {{resource_name}}
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/awscloudtrail/latest/userguide/cloudtrail-turning-off-logging.html'],
    },
    aws_cloudtrail_eds_retention: {
      title: 'CloudTrail Event Data Store Retention Period',
      description: 'The CloudTrail Event Data Store retention period should be configured appropriately.',
      serviceName: 'AWSCloudTrail',
      recommendations: ['Set an appropriate retention period for the event data store based on compliance requirements.'],
      mitigations: ['Update the event data store retention period.'],
      references: ['https://docs.aws.amazon.com/awscloudtrail/latest/userguide/query-event-data-store.html'],
    },
    aws_cloudtrail_no_multi_region_trail: {
      title: 'No Multi-Region CloudTrail Trail Exists',
      description: 'No multi-region trail is configured, meaning API activity may not be captured in all regions.',
      serviceName: 'AWSCloudTrail',
      recommendations: ['Create a multi-region trail to capture API calls across all AWS regions.'],
      mitigations: [
        'Create a trail with multi-region enabled: `aws cloudtrail create-trail --name {{resource_name}} --s3-bucket-name my-bucket --is-multi-region-trail`',
      ],
      references: ['https://docs.aws.amazon.com/awscloudtrail/latest/userguide/receive-cloudtrail-log-files-from-multiple-regions.html'],
    },
    // --- AWS DynamoDB ---
    aws_dynamodb_pitr_enabled: {
      title: 'DynamoDB Point-in-Time Recovery Should Be Enabled',
      description: 'Point-in-time recovery provides continuous backups of your DynamoDB table data.',
      serviceName: 'AmazonDynamoDB',
      recommendations: ['Enable point-in-time recovery to protect against accidental data loss.'],
      mitigations: [
        '`aws dynamodb update-continuous-backups --table-name {{resource_name}} --point-in-time-recovery-specification PointInTimeRecoveryEnabled=true`',
      ],
      references: ['https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/PointInTimeRecovery.html'],
    },
    // --- AWS Elastic Beanstalk ---
    aws_elasticbeanstalk_single_instance: {
      title: 'Elastic Beanstalk Single Instance Environment',
      description: 'The environment is running on a single instance without load balancing, which is not suitable for production workloads.',
      serviceName: 'AWSElasticBeanstalk',
      recommendations: ['Use a load-balanced environment for production workloads to ensure high availability.'],
      mitigations: ['Rebuild the environment with a load-balanced configuration.'],
      references: ['https://docs.aws.amazon.com/elasticbeanstalk/latest/dg/using-features-managing-env-types.html'],
    },
    aws_elasticbeanstalk_enhanced_health_disabled: {
      title: 'Elastic Beanstalk Enhanced Health Reporting Disabled',
      description: 'Enhanced health reporting provides detailed health information about instances and the environment.',
      serviceName: 'AWSElasticBeanstalk',
      recommendations: ['Enable enhanced health reporting for better visibility into environment health.'],
      mitigations: ['Update the environment to enable enhanced health reporting in the configuration.'],
      references: ['https://docs.aws.amazon.com/elasticbeanstalk/latest/dg/health-enhanced.html'],
    },
    // --- AWS CodeArtifact ---
    aws_codeartifact_repository_policy: {
      title: 'CodeArtifact Repository Policy Should Be Configured',
      description: 'The CodeArtifact repository does not have a resource policy configured to control access.',
      serviceName: 'AWSCodeArtifact',
      recommendations: ['Configure a resource policy on the repository to control access.'],
      mitigations: ['Set a repository permissions policy using the AWS CLI or console.'],
      references: ['https://docs.aws.amazon.com/codeartifact/latest/ug/repo-policies.html'],
    },
    // --- AWS CloudWatch ---
    aws_cloudwatch_log_group_retention: {
      title: 'CloudWatch Log Group Retention Should Be Configured',
      description: 'Log groups without a retention policy will retain logs indefinitely, increasing storage costs.',
      serviceName: 'AmazonCloudWatch',
      recommendations: ['Set a retention period for CloudWatch log groups to manage costs.'],
      mitigations: [
        `
\`\`\`
aws logs put-retention-policy --log-group-name {{recommendation.log_group_name}} --retention-in-days 90
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/Working-with-log-groups-and-streams.html'],
    },
    // --- AWS Backup ---
    aws_backup_plan_rule_lifecycle: {
      title: 'Backup Plan Rule Lifecycle Should Be Configured',
      description: 'The backup plan rule does not have a lifecycle configuration for transitioning or expiring backups.',
      serviceName: 'AWSBackup',
      recommendations: ['Configure lifecycle rules to transition backups to cold storage and expire old backups.'],
      mitigations: ['Update the backup plan rule to include lifecycle settings.'],
      references: ['https://docs.aws.amazon.com/aws-backup/latest/devguide/creating-a-backup-plan.html'],
    },
    aws_backup_plan_has_rules: {
      title: 'Backup Plan Should Have Rules',
      description: 'The backup plan does not have any backup rules configured.',
      serviceName: 'AWSBackup',
      recommendations: ['Add at least one backup rule to the backup plan.'],
      mitigations: ['Update the backup plan to include backup rules with appropriate schedules.'],
      references: ['https://docs.aws.amazon.com/aws-backup/latest/devguide/creating-a-backup-plan.html'],
    },
    // --- AWS CloudFront ---
    aws_cloudfront_access_logging: {
      title: 'CloudFront Access Logging Should Be Enabled',
      description: 'Access logging provides detailed information about every user request that CloudFront receives.',
      serviceName: 'AmazonCloudFront',
      recommendations: ['Enable access logging for your CloudFront distribution.'],
      mitigations: ['Update the distribution to enable logging and specify an S3 bucket for log storage.'],
      references: ['https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/AccessLogs.html'],
    },
    aws_cloudfront_geo_restriction: {
      title: 'CloudFront Geo-Restriction Should Be Configured',
      description: 'Geo-restriction allows you to control which countries can access your content.',
      serviceName: 'AmazonCloudFront',
      recommendations: ['Configure geo-restrictions if your content should only be accessible from specific countries.'],
      mitigations: ['Update the distribution to add geo-restriction rules.'],
      references: ['https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/georestrictions.html'],
    },
    // --- AWS X-Ray ---
    aws_xray_sampling_rules_exist: {
      title: 'X-Ray Sampling Rules Should Be Configured',
      description: 'Custom sampling rules help control the volume of trace data collected.',
      serviceName: 'AWSX-Ray',
      recommendations: ['Configure sampling rules to control trace data volume and costs.'],
      mitigations: ['Create custom sampling rules based on your application needs.'],
      references: ['https://docs.aws.amazon.com/xray/latest/devguide/xray-console-sampling.html'],
    },
    // --- AWS ECS Configuration ---
    aws_ecs_container_insights_disabled: {
      title: 'ECS Container Insights Disabled',
      description: 'Container Insights provides detailed performance metrics for ECS clusters and services.',
      serviceName: 'AmazonECS',
      recommendations: ['Enable Container Insights for better visibility into ECS performance.'],
      mitigations: [
        'Update the cluster setting: `aws ecs update-cluster-settings --cluster {{recommendation.cluster_name}} --settings name=containerInsights,value=enabled`',
      ],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/cloudwatch-container-insights.html'],
    },
    aws_ecs_avoid_default_cluster: {
      title: 'ECS Should Not Use Default Cluster',
      description: 'Using the default ECS cluster makes resource management and access control more difficult.',
      serviceName: 'AmazonECS',
      recommendations: ['Create dedicated ECS clusters for different environments and workloads.'],
      mitigations: ['Create a new cluster and migrate services from the default cluster.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/create_cluster.html'],
    },
    aws_ecs_service_min_healthy_percent_low: {
      title: 'ECS Service Minimum Healthy Percent Too Low',
      description: 'The ECS service minimum healthy percent is below 50%, which may impact availability during deployments.',
      serviceName: 'AmazonECS',
      recommendations: ['Set minimum healthy percent to at least 50% for services with multiple tasks.'],
      mitigations: ['Update the deployment configuration to set `minimumHealthyPercent` to at least 50%.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service_definition_parameters.html'],
    },
    aws_ecs_service_min_healthy_percent_too_low_for_single_task: {
      title: 'Single-Task ECS Service Minimum Healthy Percent Below 100%',
      description: 'For a single-task service, minimum healthy percent less than 100% can cause downtime during updates.',
      serviceName: 'AmazonECS',
      recommendations: ['Set minimum healthy percent to 100% for single-task services.'],
      mitigations: ['Update the deployment configuration to set `minimumHealthyPercent` to 100%.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service_definition_parameters.html'],
    },
    aws_ecs_service_max_percent_non_standard: {
      title: 'ECS Service Maximum Percent Non-Standard',
      description: 'The maximum percent deployment configuration is not set to the standard 200%.',
      serviceName: 'AmazonECS',
      recommendations: ['Review if the non-standard maximum percent is intentional.'],
      mitigations: ['Update the deployment configuration to set `maximumPercent` to 200%.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service_definition_parameters.html'],
    },
    aws_ecs_service_health_check_grace_period_zero: {
      title: 'ECS Service Health Check Grace Period Is Zero',
      description: 'The health check grace period is set to 0, which may cause tasks to be terminated before they finish starting up.',
      serviceName: 'AmazonECS',
      recommendations: ['Set an appropriate health check grace period.'],
      mitigations: ['Update the service to set `healthCheckGracePeriodSeconds` to an appropriate value.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service_definition_parameters.html'],
    },
    aws_ecs_service_exec_logging_disabled: {
      title: 'ECS Exec Logging Disabled',
      description: 'ECS Exec logging is not configured, meaning exec sessions are not being audited.',
      serviceName: 'AmazonECS',
      recommendations: ['Enable ECS Exec logging to audit interactive shell sessions.'],
      mitigations: ['Configure execute command logging in the cluster settings.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs-exec.html'],
    },
    aws_ecs_service_connect_disabled: {
      title: 'ECS Service Connect Disabled',
      description: 'ECS Service Connect is not enabled, which provides simplified service-to-service communication.',
      serviceName: 'AmazonECS',
      recommendations: ['Consider enabling ECS Service Connect for simplified service mesh capabilities.'],
      mitigations: ['Update the service to enable Service Connect with appropriate configuration.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service-connect.html'],
    },
    aws_ecs_fargate_task_definition_cpu_undefined: {
      title: 'ECS Fargate Task Definition CPU Not Defined',
      description: 'The task definition does not have CPU units defined at the task level.',
      serviceName: 'AmazonECS',
      recommendations: ['Define CPU units in the task definition.'],
      mitigations: ['Update the task definition to specify `cpu` at the task level.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html'],
    },
    aws_ecs_fargate_task_definition_memory_undefined: {
      title: 'ECS Fargate Task Definition Memory Not Defined',
      description: 'The task definition does not have memory defined at the task level.',
      serviceName: 'AmazonECS',
      recommendations: ['Define memory in the task definition.'],
      mitigations: ['Update the task definition to specify `memory` at the task level.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html'],
    },
    aws_ecs_task_definition_missing_task_role: {
      title: 'ECS Task Definition Missing Task Role',
      description: 'The task definition does not have an IAM task role assigned.',
      serviceName: 'AmazonECS',
      recommendations: ['Assign an IAM task role following the principle of least privilege.'],
      mitigations: ['Update the task definition to include a `taskRoleArn`.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-iam-roles.html'],
    },
    aws_ecs_task_definition_image_latest_tag: {
      title: 'ECS Task Definition Using Latest Image Tag',
      description: 'The container image uses the :latest tag, making deployments non-deterministic.',
      serviceName: 'AmazonECS',
      recommendations: ['Use specific image tags or digests instead of :latest.'],
      mitigations: ['Update the container definition to use a specific image tag.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html'],
    },
    aws_ecs_task_definition_logging_not_configured: {
      title: 'ECS Task Definition Logging Not Configured',
      description: 'The container does not have a log driver configured.',
      serviceName: 'AmazonECS',
      recommendations: ['Configure logging for all containers in the task definition.'],
      mitigations: ['Add a `logConfiguration` section with an appropriate log driver.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/using_awslogs.html'],
    },
    aws_ecs_task_definition_health_check_not_configured: {
      title: 'ECS Task Definition Health Check Not Configured',
      description: 'The container does not have a health check configured.',
      serviceName: 'AmazonECS',
      recommendations: ['Configure health checks for containers.'],
      mitigations: ['Add a `healthCheck` section to the container definition.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html#container_definition_healthcheck'],
    },
    aws_ecs_service_review_autoscaling: {
      title: 'ECS Service Should Review Autoscaling Configuration',
      description: 'The ECS service autoscaling configuration should be reviewed to ensure it meets workload demands.',
      serviceName: 'AmazonECS',
      recommendations: ['Review and configure autoscaling policies for the ECS service.'],
      mitigations: ['Set up Application Auto Scaling with appropriate target tracking or step scaling policies.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service-auto-scaling.html'],
    },
    // --- AWS EFS ---
    aws_efs_lifecycle_policy: {
      title: 'EFS Lifecycle Policy Should Be Configured',
      description: 'A lifecycle policy automatically moves files to the Infrequent Access storage class to reduce costs.',
      serviceName: 'AmazonEFS',
      recommendations: ['Configure a lifecycle policy to move infrequently accessed files to IA storage.'],
      mitigations: [
        `
\`\`\`
aws efs put-lifecycle-configuration --file-system-id fs-xxx --lifecycle-policies TransitionToIA=AFTER_30_DAYS
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/efs/latest/ug/lifecycle-management-efs.html'],
    },
    // --- AWS SNS ---
    aws_sns_delivery_status_logging: {
      title: 'SNS Topic Delivery Status Logging Should Be Enabled',
      description: 'Delivery status logging helps track the delivery status of messages published to the topic.',
      serviceName: 'AmazonSNS',
      recommendations: ['Enable delivery status logging for the SNS topic.'],
      mitigations: ['Configure delivery status logging attributes on the SNS topic.'],
      references: ['https://docs.aws.amazon.com/sns/latest/dg/sns-topic-attributes.html'],
    },
    // --- AWS SES ---
    aws_ses_identity_dkim: {
      title: 'SES Identity DKIM Should Be Configured',
      description: 'DKIM signing adds a digital signature to outgoing emails, improving deliverability and authentication.',
      serviceName: 'AmazonSES',
      recommendations: ['Enable DKIM signing for all SES identities.'],
      mitigations: [
        `
**Configure DKIM for the identity:**
\`\`\`
aws ses verify-domain-dkim --domain example.com
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/ses/latest/dg/send-email-authentication-dkim.html'],
    },
    aws_ses_identity_mail_from_domain: {
      title: 'SES Identity Mail From Domain Should Be Configured',
      description: 'Configuring a custom MAIL FROM domain improves email deliverability and sender reputation.',
      serviceName: 'AmazonSES',
      recommendations: ['Configure a custom MAIL FROM domain for the SES identity.'],
      mitigations: ['Set the MAIL FROM domain in the SES identity configuration.'],
      references: ['https://docs.aws.amazon.com/ses/latest/dg/mail-from.html'],
    },
    aws_ses_identity_notifications: {
      title: 'SES Identity Notifications Should Be Configured',
      description: 'Notification configuration ensures you are alerted about bounces, complaints, and delivery issues.',
      serviceName: 'AmazonSES',
      recommendations: ['Configure SNS notifications for bounces and complaints on SES identities.'],
      mitigations: ['Set up notification topics for the SES identity.'],
      references: ['https://docs.aws.amazon.com/ses/latest/dg/monitor-sending-activity.html'],
    },
    aws_ses_configset_event_destinations: {
      title: 'SES Configuration Set Should Have Event Destinations',
      description: 'Event destinations allow you to publish email sending events to CloudWatch, Kinesis, or SNS.',
      serviceName: 'AmazonSES',
      recommendations: ['Configure event destinations for your SES configuration sets.'],
      mitigations: ['Add event destinations to the configuration set.'],
      references: ['https://docs.aws.amazon.com/ses/latest/dg/event-publishing.html'],
    },
    // --- AWS Config ---
    aws_config_recorder_not_recording: {
      title: 'AWS Config Recorder Not Recording',
      description: 'The AWS Config recorder is not actively recording resource changes.',
      serviceName: 'AWSConfig',
      recommendations: ['Start the Config recorder to track resource configuration changes.'],
      mitigations: [
        `
\`\`\`
aws configservice start-configuration-recorder --configuration-recorder-name default
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/config/latest/developerguide/stop-start-recorder.html'],
    },
    aws_config_no_delivery_channel: {
      title: 'AWS Config No Delivery Channel',
      description: 'No delivery channel is configured for AWS Config, so configuration snapshots cannot be delivered.',
      serviceName: 'AWSConfig',
      recommendations: ['Create a delivery channel for AWS Config.'],
      mitigations: ['Configure a delivery channel with an S3 bucket.'],
      references: ['https://docs.aws.amazon.com/config/latest/developerguide/manage-delivery-channel.html'],
    },
    aws_config_not_recording_all_resources: {
      title: 'AWS Config Not Recording All Resources',
      description: 'AWS Config is not configured to record all resource types.',
      serviceName: 'AWSConfig',
      recommendations: ['Configure the Config recorder to record all supported resource types.'],
      mitigations: ['Update the recording group to include all resource types.'],
      references: ['https://docs.aws.amazon.com/config/latest/developerguide/select-resources.html'],
    },
    aws_config_rule_non_compliant: {
      title: 'AWS Config Rule Non-Compliant',
      description: 'One or more AWS Config rules have non-compliant resources.',
      serviceName: 'AWSConfig',
      recommendations: ['Review and remediate non-compliant resources.'],
      mitigations: ['Review the non-compliant resources and apply remediation actions.'],
      references: ['https://docs.aws.amazon.com/config/latest/developerguide/evaluate-config.html'],
    },
    aws_config_rule_evaluation_error: {
      title: 'AWS Config Rule Evaluation Error',
      description: 'An AWS Config rule has evaluation errors, meaning it cannot properly assess compliance.',
      serviceName: 'AWSConfig',
      recommendations: ['Investigate and fix the rule evaluation error.'],
      mitigations: ['Check the rule permissions and configuration.'],
      references: ['https://docs.aws.amazon.com/config/latest/developerguide/evaluate-config.html'],
    },
    aws_config_conformance_pack_non_compliant: {
      title: 'AWS Config Conformance Pack Non-Compliant',
      description: 'A Config conformance pack has non-compliant rules.',
      serviceName: 'AWSConfig',
      recommendations: ['Review and remediate non-compliant rules in the conformance pack.'],
      mitigations: ['Address the specific non-compliant rules within the conformance pack.'],
      references: ['https://docs.aws.amazon.com/config/latest/developerguide/conformance-packs.html'],
    },
    aws_config_not_enabled: {
      title: 'AWS Config Not Enabled',
      description:
        'AWS Config is not enabled in this region. AWS Config provides a detailed inventory of your AWS resources and their configurations, records configuration changes, and evaluates resource configurations against desired settings for compliance auditing.',
      serviceName: 'AWSConfig',
      recommendations: ['Enable AWS Config to track resource configuration changes and compliance.'],
      mitigations: ['Set up AWS Config with a recorder, delivery channel, and appropriate rules.'],
      references: ['https://docs.aws.amazon.com/config/latest/developerguide/getting-started.html'],
    },
    // --- AWS SQS ---
    aws_sqs_dlq_configured: {
      title: 'SQS Dead Letter Queue Should Be Configured',
      description: 'A dead letter queue captures messages that cannot be processed successfully for later analysis.',
      serviceName: 'AmazonSQS',
      recommendations: ['Configure a dead letter queue for your SQS queues to handle failed messages.'],
      mitigations: ['Set the `RedrivePolicy` attribute on the SQS queue with a DLQ ARN and `maxReceiveCount`.'],
      references: ['https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html'],
    },
    // --- AWS Bedrock ---
    aws_bedrock_invocation_logging_check_failed: {
      title: 'Bedrock Invocation Logging Check Failed',
      description: 'Unable to verify if invocation logging is enabled for Amazon Bedrock.',
      serviceName: 'AmazonBedrock',
      recommendations: ['Verify that invocation logging is properly configured for Bedrock.'],
      mitigations: ['Configure invocation logging in the Bedrock console or via API.'],
      references: ['https://docs.aws.amazon.com/bedrock/latest/userguide/model-invocation-logging.html'],
    },
    aws_bedrock_invocation_logging_enabled: {
      title: 'Bedrock Invocation Logging Should Be Enabled',
      description: 'Invocation logging captures details about model invocations for auditing and debugging.',
      serviceName: 'AmazonBedrock',
      recommendations: ['Enable invocation logging for Amazon Bedrock to track model usage.'],
      mitigations: ['Configure invocation logging with S3 or CloudWatch Logs as the destination.'],
      references: ['https://docs.aws.amazon.com/bedrock/latest/userguide/model-invocation-logging.html'],
    },
    // --- AWS SSM ---
    aws_ssm_instance_offline: {
      title: 'SSM Managed Instance Offline',
      description: 'The SSM-managed instance is not reporting to Systems Manager.',
      serviceName: 'AWSSystemsManager',
      recommendations: ['Investigate why the instance is not communicating with Systems Manager.'],
      mitigations: ['Verify the SSM agent is running and the instance has appropriate IAM permissions and network connectivity.'],
      references: ['https://docs.aws.amazon.com/systems-manager/latest/userguide/fleet-manager.html'],
    },
    aws_ssm_missing_patches: {
      title: 'SSM Instance Has Missing Patches',
      description: 'The instance has patches that have not been applied per the patch baseline.',
      serviceName: 'AWSSystemsManager',
      recommendations: ['Apply missing patches to maintain security compliance.'],
      mitigations: [
        `
**Run a patch operation:**
\`\`\`
aws ssm send-command --document-name AWS-RunPatchBaseline --targets Key=instanceids,Values=i-xxx
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/systems-manager/latest/userguide/patch-manager.html'],
    },
    aws_ssm_no_associations: {
      title: 'SSM Instance Has No Associations',
      description: 'The instance has no SSM State Manager associations configured.',
      serviceName: 'AWSSystemsManager',
      recommendations: ['Configure SSM associations for automated management tasks.'],
      mitigations: ['Create associations for patch management, inventory collection, or other management tasks.'],
      references: ['https://docs.aws.amazon.com/systems-manager/latest/userguide/systems-manager-associations.html'],
    },
    aws_ssm_outdated_agent: {
      title: 'SSM Agent Is Outdated',
      description: 'The SSM agent on the instance is not the latest version.',
      serviceName: 'AWSSystemsManager',
      recommendations: ['Update the SSM agent to the latest version.'],
      mitigations: [
        `
**Update via SSM:**
\`\`\`
aws ssm send-command --document-name AWS-UpdateSSMAgent --targets Key=instanceids,Values=i-xxx
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/systems-manager/latest/userguide/ssm-agent.html'],
    },
    aws_ssm_maintenance_window_disabled: {
      title: 'SSM Maintenance Window Disabled',
      description: 'The maintenance window is disabled, so scheduled tasks will not execute.',
      serviceName: 'AWSSystemsManager',
      recommendations: ['Enable the maintenance window if scheduled maintenance tasks are needed.'],
      mitigations: ['Enable the maintenance window via console or CLI.'],
      references: ['https://docs.aws.amazon.com/systems-manager/latest/userguide/systems-manager-maintenance.html'],
    },
    // --- GCP GKE Configuration ---
    gcp_gke_no_labels: {
      title: 'GKE Cluster Has No Labels',
      description: 'The GKE cluster does not have labels for resource organization and cost tracking.',
      serviceName: 'GKE',
      recommendations: ['Add labels to GKE clusters for better organization, cost allocation, and management.'],
      mitigations: [
        `
\`\`\`
gcloud container clusters update CLUSTER_NAME --update-labels key=value
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/kubernetes-engine/docs/how-to/creating-managing-labels'],
    },
    gcp_gke_no_autoscaling: {
      title: 'GKE Node Pool Autoscaling Disabled',
      description: 'The GKE node pool does not have autoscaling enabled.',
      serviceName: 'GKE',
      recommendations: ['Enable cluster autoscaler to automatically adjust the number of nodes based on workload.'],
      mitigations: [
        `
\`\`\`
gcloud container clusters update CLUSTER --enable-autoscaling --min-nodes=1 --max-nodes=10 --node-pool=POOL
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/kubernetes-engine/docs/concepts/cluster-autoscaler'],
    },
    gcp_gke_no_binary_authorization: {
      title: 'GKE Binary Authorization Disabled',
      description: 'Binary Authorization is not enabled, so container image deployments are not verified.',
      serviceName: 'GKE',
      recommendations: ['Enable Binary Authorization to ensure only trusted container images are deployed.'],
      mitigations: ['Enable Binary Authorization in the cluster configuration.'],
      references: ['https://cloud.google.com/binary-authorization/docs/overview'],
    },
    gcp_gke_no_network_policy: {
      title: 'GKE Network Policy Disabled',
      description: 'Network policy enforcement is not enabled on the GKE cluster.',
      serviceName: 'GKE',
      recommendations: ['Enable network policy enforcement to control pod-to-pod communication.'],
      mitigations: [
        `
\`\`\`
gcloud container clusters update CLUSTER --enable-network-policy
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/kubernetes-engine/docs/how-to/network-policy'],
    },
    gcp_gke_no_maintenance_window: {
      title: 'GKE No Maintenance Window Configured',
      description: 'No maintenance window is configured for the GKE cluster.',
      serviceName: 'GKE',
      recommendations: ['Configure a maintenance window to control when cluster upgrades occur.'],
      mitigations: [
        `
**Set a maintenance window:**
\`\`\`
gcloud container clusters update CLUSTER --maintenance-window-start=...
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/kubernetes-engine/docs/how-to/maintenance-windows-and-exclusions'],
    },
    gcp_gke_no_workload_identity: {
      title: 'GKE Workload Identity Disabled',
      description: 'Workload Identity is not enabled, which is the recommended way for pods to access Google Cloud services.',
      serviceName: 'GKE',
      recommendations: ['Enable Workload Identity for secure pod-to-GCP-service authentication.'],
      mitigations: [
        `
\`\`\`
gcloud container clusters update CLUSTER --workload-pool=PROJECT_ID.svc.id.goog
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/kubernetes-engine/docs/how-to/workload-identity'],
    },
    gcp_gke_logging_disabled: {
      title: 'GKE Logging Disabled',
      description:
        'Cloud Logging is not enabled for this GKE cluster. Without logging, critical cluster events, pod logs, and system component logs are not captured, making troubleshooting and security monitoring impossible.',
      serviceName: 'GKE',
      recommendations: ['Enable Cloud Logging for the GKE cluster for monitoring and troubleshooting.'],
      mitigations: [
        `
\`\`\`
gcloud container clusters update CLUSTER --logging=SYSTEM,WORKLOAD
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/kubernetes-engine/docs/how-to/logging'],
    },
    gcp_gke_monitoring_disabled: {
      title: 'GKE Monitoring Disabled',
      description: 'Cloud Monitoring is not enabled for the GKE cluster.',
      serviceName: 'GKE',
      recommendations: ['Enable Cloud Monitoring for the GKE cluster for performance insights.'],
      mitigations: [
        `
\`\`\`
gcloud container clusters update CLUSTER --monitoring=SYSTEM,WORKLOAD
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/kubernetes-engine/docs/how-to/monitoring'],
    },
    // --- GCP Cloud Functions ---
    gcp_function_no_labels: {
      title: 'Cloud Function Has No Labels',
      description: 'The Cloud Function does not have labels for organization and cost tracking.',
      serviceName: 'CloudFunctions',
      recommendations: ['Add labels to Cloud Functions for better resource management.'],
      mitigations: ['Update the function with labels via console or CLI.'],
      references: ['https://cloud.google.com/functions/docs/configuring'],
    },
    // --- GCP Pub/Sub ---
    gcp_pubsub_no_dead_letter: {
      title: 'Pub/Sub Subscription Has No Dead Letter Topic',
      description: 'The subscription does not have a dead letter topic configured for handling undeliverable messages.',
      serviceName: 'CloudPubSub',
      recommendations: ['Configure a dead letter topic to capture messages that cannot be processed.'],
      mitigations: ['Update the subscription to add a dead letter policy.'],
      references: ['https://cloud.google.com/pubsub/docs/dead-letter-topics'],
    },
    gcp_pubsub_long_retention: {
      title: 'Pub/Sub Subscription Has Long Retention Period',
      description: 'The subscription has an unusually long message retention period which may increase costs.',
      serviceName: 'CloudPubSub',
      recommendations: ['Review if the long retention period is necessary and reduce if possible.'],
      mitigations: ['Update the subscription retention duration.'],
      references: ['https://cloud.google.com/pubsub/docs/subscriber'],
    },
    gcp_pubsub_retain_acked: {
      title: 'Pub/Sub Subscription Retains Acknowledged Messages',
      description: 'The subscription is configured to retain acknowledged messages, increasing storage costs.',
      serviceName: 'CloudPubSub',
      recommendations: ['Review if retaining acknowledged messages is necessary.'],
      mitigations: ['Disable retain_acked_messages if not needed.'],
      references: ['https://cloud.google.com/pubsub/docs/subscriber'],
    },
    // --- GCP BigQuery ---
    gcp_bigquery_dataset_no_labels: {
      title: 'BigQuery Dataset Has No Labels',
      description: 'The BigQuery dataset does not have labels for organization and cost tracking.',
      serviceName: 'BigQuery',
      recommendations: ['Add labels to BigQuery datasets for better resource management.'],
      mitigations: ['Update the dataset with labels via console or CLI.'],
      references: ['https://cloud.google.com/bigquery/docs/adding-labels'],
    },
    gcp_bigquery_table_no_labels: {
      title: 'BigQuery Table Has No Labels',
      description:
        'The BigQuery table does not have labels applied. Labels are key-value pairs used for organizing, filtering, and tracking costs across Google Cloud resources.',
      serviceName: 'BigQuery',
      recommendations: ['Add labels to BigQuery tables for organization.'],
      mitigations: ['Update the table with labels.'],
      references: ['https://cloud.google.com/bigquery/docs/adding-labels'],
    },
    gcp_bigquery_table_no_expiration: {
      title: 'BigQuery Table Has No Expiration',
      description: 'The table does not have an expiration time set, which may lead to accumulating unused data.',
      serviceName: 'BigQuery',
      recommendations: ['Set a table expiration for temporary or time-limited data.'],
      mitigations: [
        `
Set the table expiration using
\`\`\`
bq update --expiration SECONDS DATASET.TABLE
\`\`\`
.
`,
      ],
      references: ['https://cloud.google.com/bigquery/docs/table-expiration-times'],
    },
    gcp_bigquery_dataset_no_default_expiration: {
      title: 'BigQuery Dataset Has No Default Table Expiration',
      description: 'The dataset does not have a default table expiration configured.',
      serviceName: 'BigQuery',
      recommendations: ['Set a default table expiration for the dataset to automatically clean up old tables.'],
      mitigations: [
        `
\`\`\`
bq update --default_table_expiration SECONDS DATASET
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/bigquery/docs/table-expiration-times'],
    },
    gcp_bigquery_table_no_partitioning: {
      title: 'BigQuery Table Not Partitioned',
      description: 'The table is not partitioned, which can lead to higher query costs and slower performance.',
      serviceName: 'BigQuery',
      recommendations: ['Use table partitioning to improve query performance and reduce costs.'],
      mitigations: ['Recreate the table with partitioning (e.g., by ingestion time or a date column).'],
      references: ['https://cloud.google.com/bigquery/docs/partitioned-tables'],
    },
    gcp_bigquery_table_no_clustering: {
      title: 'BigQuery Table Not Clustered',
      description: 'The table is not clustered, which can lead to less efficient queries.',
      serviceName: 'BigQuery',
      recommendations: ['Use clustering on frequently filtered or aggregated columns to improve performance.'],
      mitigations: ['Recreate the table with clustering on appropriate columns.'],
      references: ['https://cloud.google.com/bigquery/docs/clustered-tables'],
    },
    // --- GCP Cloud Run ---
    gcp_run_no_labels: {
      title: 'Cloud Run Service Has No Labels',
      description: 'The Cloud Run service does not have labels for organization.',
      serviceName: 'CloudRun',
      recommendations: ['Add labels to Cloud Run services for better management.'],
      mitigations: ['Update the service with labels.'],
      references: ['https://cloud.google.com/run/docs/configuring/labels'],
    },
    gcp_run_always_on: {
      title: 'Cloud Run Service Always On',
      description: 'The service has minimum instances set above 0, meaning it is always running and incurring costs.',
      serviceName: 'CloudRun',
      recommendations: ['Review if always-on is necessary. Set min instances to 0 if cold starts are acceptable.'],
      mitigations: ['Update the service to set min instances to 0 for cost savings.'],
      references: ['https://cloud.google.com/run/docs/configuring/cpu-allocation'],
    },
    gcp_run_not_ready: {
      title: 'Cloud Run Service Not Ready',
      description:
        'The Cloud Run service is not in a ready state. This may indicate deployment failures, container startup issues, or configuration problems that prevent the service from handling traffic.',
      serviceName: 'CloudRun',
      recommendations: ['Investigate why the service is not ready and resolve deployment issues.'],
      mitigations: ['Check service logs and deployment status for errors.'],
      references: ['https://cloud.google.com/run/docs/managing/services'],
    },
    // --- GCP Compute ---
    gcp_compute_no_labels: {
      title: 'Compute Instance Has No Labels',
      description: 'The Compute Engine instance does not have labels for organization.',
      serviceName: 'ComputeEngine',
      recommendations: ['Add labels to instances for resource management and cost tracking.'],
      mitigations: [
        `
\`\`\`
gcloud compute instances update INSTANCE --update-labels key=value
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/compute/docs/labeling-resources'],
    },
    // --- GCP Cloud SQL ---
    gcp_sql_no_labels: {
      title: 'Cloud SQL Instance Has No Labels',
      description:
        'The Cloud SQL instance does not have labels applied. Labels help organize resources, track costs, and enforce governance policies across your GCP environment.',
      serviceName: 'CloudSQL',
      recommendations: ['Add labels for organization and cost tracking.'],
      mitigations: ['Update the instance with labels via console or CLI.'],
      references: ['https://cloud.google.com/sql/docs/mysql/label-instance'],
    },
    gcp_sql_no_backup: {
      title: 'Cloud SQL Automated Backup Disabled',
      description: 'Automated backups are not enabled for the Cloud SQL instance.',
      serviceName: 'CloudSQL',
      recommendations: ['Enable automated backups to protect against data loss.'],
      mitigations: [
        `
\`\`\`
gcloud sql instances patch INSTANCE --backup-start-time=HH:MM
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/sql/docs/mysql/backup-recovery/backups'],
    },
    gcp_sql_no_ha: {
      title: 'Cloud SQL High Availability Disabled',
      description: 'The Cloud SQL instance does not have high availability configured.',
      serviceName: 'CloudSQL',
      recommendations: ['Enable high availability for production databases.'],
      mitigations: [
        `
\`\`\`
gcloud sql instances patch INSTANCE --availability-type=REGIONAL
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/sql/docs/mysql/high-availability'],
    },
    // --- GCP Load Balancing ---
    gcp_lb_backend_no_health_check: {
      title: 'Load Balancer Backend Has No Health Check',
      description: 'The backend service does not have a health check configured.',
      serviceName: 'LoadBalancing',
      recommendations: ['Configure a health check for the backend service.'],
      mitigations: ['Create and attach a health check to the backend service.'],
      references: ['https://cloud.google.com/load-balancing/docs/health-check-concepts'],
    },
    gcp_lb_backend_no_backends: {
      title: 'Load Balancer Backend Has No Backend Instances',
      description: 'The backend service has no backend instance groups or NEGs attached.',
      serviceName: 'LoadBalancing',
      recommendations: ['Attach backend instances to the backend service.'],
      mitigations: ['Add instance groups or network endpoint groups to the backend service.'],
      references: ['https://cloud.google.com/load-balancing/docs/backend-service'],
    },
    gcp_lb_no_labels: {
      title: 'Load Balancer Forwarding Rule Has No Labels',
      description:
        'The GCP load balancer forwarding rule does not have labels applied. Labels help identify and organize networking resources for cost tracking and governance.',
      serviceName: 'LoadBalancing',
      recommendations: ['Add labels for resource organization.'],
      mitigations: ['Update the forwarding rule with labels.'],
      references: ['https://cloud.google.com/compute/docs/labeling-resources'],
    },
    // --- GCP Cloud Storage ---
    gcp_storage_no_labels: {
      title: 'Cloud Storage Bucket Has No Labels',
      description: 'The storage bucket does not have labels for organization.',
      serviceName: 'CloudStorage',
      recommendations: ['Add labels to buckets for better management and cost tracking.'],
      mitigations: [
        `
\`\`\`
gsutil label ch -l key:value gs://BUCKET_NAME
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/storage/docs/tags-and-labels'],
    },
    gcp_storage_no_versioning: {
      title: 'Cloud Storage Versioning Disabled',
      description: 'Object versioning is not enabled, so deleted or overwritten objects cannot be recovered.',
      serviceName: 'CloudStorage',
      recommendations: ['Enable versioning to protect against accidental deletion or overwrite.'],
      mitigations: [
        `
\`\`\`
gsutil versioning set on gs://BUCKET_NAME
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/storage/docs/object-versioning'],
    },
    gcp_storage_no_lifecycle: {
      title: 'Cloud Storage No Lifecycle Policy',
      description: 'The bucket does not have a lifecycle policy to manage object lifecycle.',
      serviceName: 'CloudStorage',
      recommendations: ['Configure lifecycle rules to automatically delete or transition old objects.'],
      mitigations: [
        `
Set lifecycle rules via console or
\`\`\`
gsutil lifecycle set config.json gs://BUCKET_NAME
\`\`\`
.
`,
      ],
      references: ['https://cloud.google.com/storage/docs/lifecycle'],
    },
    gcp_storage_no_logging: {
      title: 'Cloud Storage Access Logging Disabled',
      description: 'Access logging is not enabled for the storage bucket.',
      serviceName: 'CloudStorage',
      recommendations: ['Enable access logging to track bucket access for auditing.'],
      mitigations: [
        `
\`\`\`
gsutil logging set on -b gs://LOG_BUCKET gs://BUCKET_NAME
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/storage/docs/access-logs'],
    },
    // --- Azure Configuration ---
    azure_missing_tags: {
      title: 'Azure Resource Missing Tags',
      description: 'The Azure resource does not have tags for organization, cost tracking, and management.',
      serviceName: 'Azure',
      recommendations: ['Add tags to all resources for better organization and cost management.'],
      mitigations: [
        `
**Add tags via Azure CLI:**
\`\`\`
az resource tag --tags key=value --ids RESOURCE_ID
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources'],
    },
    azure_monitor_alert_rule_disabled: {
      title: 'Azure Monitor Alert Rule Disabled',
      description: 'An Azure Monitor alert rule is disabled and will not trigger alerts.',
      serviceName: 'AzureMonitor',
      recommendations: ['Enable the alert rule or remove it if no longer needed.'],
      mitigations: ['Enable the alert rule via Azure portal or CLI.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-manage-alert-rules'],
    },
    azure_monitor_alert_no_action_group: {
      title: 'Azure Monitor Alert Has No Action Group',
      description: 'The alert rule does not have an action group configured for notifications.',
      serviceName: 'AzureMonitor',
      recommendations: ['Attach an action group to receive notifications when the alert fires.'],
      mitigations: ['Update the alert rule to include an action group.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/action-groups'],
    },
    azure_monitor_action_group_disabled: {
      title: 'Azure Monitor Action Group Disabled',
      description: 'The action group is disabled and will not send notifications.',
      serviceName: 'AzureMonitor',
      recommendations: ['Enable the action group or remove it if no longer needed.'],
      mitigations: ['Enable the action group via Azure portal.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/action-groups'],
    },
    azure_monitor_action_group_no_receivers: {
      title: 'Azure Monitor Action Group Has No Receivers',
      description: 'The action group does not have any notification receivers configured.',
      serviceName: 'AzureMonitor',
      recommendations: ['Add at least one receiver (email, SMS, webhook, etc.) to the action group.'],
      mitigations: ['Update the action group to add notification receivers.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/action-groups'],
    },
    azure_monitor_no_action_groups: {
      title: 'No Azure Monitor Action Groups Configured',
      description: 'No action groups are configured in the subscription for alert notifications.',
      serviceName: 'AzureMonitor',
      recommendations: ['Create action groups to receive alert notifications.'],
      mitigations: ['Create an action group with appropriate notification receivers.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/action-groups'],
    },
    azure_monitor_no_alert_rules: {
      title: 'No Azure Monitor Alert Rules Configured',
      description:
        'No Azure Monitor alert rules are configured. Without alert rules, critical issues such as high CPU, memory pressure, or service failures will not trigger notifications.',
      serviceName: 'AzureMonitor',
      recommendations: ['Create alert rules to monitor critical resources and metrics.'],
      mitigations: ['Set up alert rules for important metrics and log conditions.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-overview'],
    },
    azure_cosmosdb_automatic_failover_disabled: {
      title: 'Cosmos DB Automatic Failover Disabled',
      description: 'Automatic failover is not enabled for the Cosmos DB account.',
      serviceName: 'AzureCosmosDB',
      recommendations: ['Enable automatic failover for high availability.'],
      mitigations: ['Enable automatic failover in the Cosmos DB account settings.'],
      references: ['https://learn.microsoft.com/en-us/azure/cosmos-db/high-availability'],
    },
    azure_cosmosdb_single_region: {
      title: 'Cosmos DB Single Region Deployment',
      description: 'The Cosmos DB account is deployed in a single region without geo-replication.',
      serviceName: 'AzureCosmosDB',
      recommendations: ['Add additional read regions for high availability and disaster recovery.'],
      mitigations: ['Add geo-replicated regions in the Cosmos DB account configuration.'],
      references: ['https://learn.microsoft.com/en-us/azure/cosmos-db/distribute-data-globally'],
    },
    azure_recovery_vault_soft_delete_disabled: {
      title: 'Recovery Vault Soft Delete Disabled',
      description: 'Soft delete is not enabled, so deleted backup data cannot be recovered.',
      serviceName: 'AzureRecoveryServices',
      recommendations: ['Enable soft delete to protect against accidental deletion of backups.'],
      mitigations: ['Enable soft delete in the Recovery Services vault settings.'],
      references: ['https://learn.microsoft.com/en-us/azure/backup/backup-azure-security-feature-cloud'],
    },
    azure_recovery_vault_lrs_storage: {
      title: 'Recovery Vault Using LRS Storage',
      description: 'The Recovery Services vault is using Locally Redundant Storage instead of Geo-Redundant Storage.',
      serviceName: 'AzureRecoveryServices',
      recommendations: ['Consider using GRS for production vaults to protect against regional outages.'],
      mitigations: ['Update the vault storage replication type to GRS.'],
      references: ['https://learn.microsoft.com/en-us/azure/backup/backup-create-recovery-services-vault'],
    },
    azure_sql_geo_redundant_backups_disabled: {
      title: 'Azure SQL Geo-Redundant Backups Disabled',
      description: 'Geo-redundant backups are not enabled for the SQL database.',
      serviceName: 'AzureSQL',
      recommendations: ['Enable geo-redundant backups for disaster recovery.'],
      mitigations: ['Update the database backup settings to enable geo-redundancy.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-sql/database/automated-backups-overview'],
    },
    azure_sql_long_term_retention_not_configured: {
      title: 'Azure SQL Long-Term Retention Not Configured',
      description: 'Long-term backup retention is not configured for the SQL database.',
      serviceName: 'AzureSQL',
      recommendations: ['Configure long-term retention to keep backups beyond the default retention period.'],
      mitigations: ['Set up long-term retention policies in the database backup configuration.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-sql/database/long-term-retention-overview'],
    },
    azure_sql_storage_auto_growth_disabled: {
      title: 'Azure SQL Storage Auto-Growth Disabled',
      description: 'Automatic storage growth is not enabled, which may cause the database to run out of space.',
      serviceName: 'AzureSQL',
      recommendations: ['Enable automatic storage growth for the database.'],
      mitigations: ['Enable auto-grow in the database server configuration.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-sql/database/resource-limits-logical-server'],
    },
    azure_postgres_backup_disabled: {
      title: 'Azure PostgreSQL Backup Disabled',
      description: 'Backup is not properly configured for the PostgreSQL server.',
      serviceName: 'AzurePostgreSQL',
      recommendations: ['Ensure backups are enabled and configured with appropriate retention.'],
      mitigations: ['Configure backup settings for the PostgreSQL server.'],
      references: ['https://learn.microsoft.com/en-us/azure/postgresql/flexible-server/concepts-backup-restore'],
    },
    azure_postgres_server_stopped: {
      title: 'Azure PostgreSQL Server Stopped',
      description:
        'The Azure Database for PostgreSQL server is in a stopped state. Stopped servers still incur storage charges. If the server is no longer needed, consider deleting it to eliminate costs.',
      serviceName: 'AzurePostgreSQL',
      recommendations: ['Review if the server should be running. Stopped servers may still incur storage costs.'],
      mitigations: ['Start the server if needed, or delete it if no longer required.'],
      references: ['https://learn.microsoft.com/en-us/azure/postgresql/flexible-server/how-to-stop-start-server-portal'],
    },
    azure_mysql_backup_disabled: {
      title: 'Azure MySQL Backup Disabled',
      description: 'Backup is not properly configured for the MySQL server.',
      serviceName: 'AzureMySQL',
      recommendations: ['Ensure backups are enabled with appropriate retention.'],
      mitigations: ['Configure backup settings for the MySQL server.'],
      references: ['https://learn.microsoft.com/en-us/azure/mysql/flexible-server/concepts-backup-restore'],
    },
    azure_mysql_server_stopped: {
      title: 'Azure MySQL Server Stopped',
      description:
        'The Azure Database for MySQL server is in a stopped state. Stopped servers still incur storage charges. If the server is no longer needed, consider deleting it to eliminate costs.',
      serviceName: 'AzureMySQL',
      recommendations: ['Review if the server should be running.'],
      mitigations: ['Start the server or delete it if no longer required.'],
      references: ['https://learn.microsoft.com/en-us/azure/mysql/flexible-server/how-to-stop-start-server'],
    },
    azure_mariadb_backup_disabled: {
      title: 'Azure MariaDB Backup Disabled',
      description: 'Backup is not properly configured for the MariaDB server.',
      serviceName: 'AzureMariaDB',
      recommendations: ['Ensure backups are enabled with appropriate retention.'],
      mitigations: ['Configure backup settings for the MariaDB server.'],
      references: ['https://learn.microsoft.com/en-us/azure/mariadb/concepts-backup'],
    },
    azure_mariadb_server_stopped: {
      title: 'Azure MariaDB Server Stopped',
      description:
        'The Azure Database for MariaDB server is in a stopped state. Stopped servers still incur storage charges. If the server is no longer needed, consider deleting it.',
      serviceName: 'AzureMariaDB',
      recommendations: ['Review if the server should be running.'],
      mitigations: ['Start the server or delete it if no longer required.'],
      references: ['https://learn.microsoft.com/en-us/azure/mariadb/howto-stop-start-server'],
    },
    azure_load_balancer_no_health_probes: {
      title: 'Azure Load Balancer Has No Health Probes',
      description: 'The load balancer does not have health probes configured.',
      serviceName: 'AzureLoadBalancer',
      recommendations: ['Configure health probes to detect unhealthy backend instances.'],
      mitigations: ['Add health probes to the load balancer configuration.'],
      references: ['https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-custom-probe-overview'],
    },
    azure_load_balancer_no_outbound_rules: {
      title: 'Azure Load Balancer Has No Outbound Rules',
      description: 'The load balancer does not have outbound rules for SNAT.',
      serviceName: 'AzureLoadBalancer',
      recommendations: ['Configure outbound rules for backend pool outbound connectivity.'],
      mitigations: ['Add outbound rules to the load balancer.'],
      references: ['https://learn.microsoft.com/en-us/azure/load-balancer/outbound-rules'],
    },
    azure_load_balancer_basic_sku: {
      title: 'Azure Load Balancer Using Basic SKU',
      description: 'The load balancer is using the Basic SKU which has limited features and will be retired.',
      serviceName: 'AzureLoadBalancer',
      recommendations: ['Upgrade to Standard SKU for better features and SLA.'],
      mitigations: ['Migrate to a Standard SKU load balancer.'],
      references: ['https://learn.microsoft.com/en-us/azure/load-balancer/skus'],
    },
    azure_vmss_automatic_instance_repairs_disabled: {
      title: 'VMSS Automatic Instance Repairs Disabled',
      description: 'Automatic instance repairs are not enabled for the VM scale set.',
      serviceName: 'AzureVMSS',
      recommendations: ['Enable automatic instance repairs to replace unhealthy instances automatically.'],
      mitigations: ['Enable the automatic repairs policy on the scale set.'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/virtual-machine-scale-sets-automatic-instance-repairs'],
    },
    azure_vmss_not_zone_redundant: {
      title: 'VMSS Not Zone Redundant',
      description: 'The VM scale set is not deployed across availability zones.',
      serviceName: 'AzureVMSS',
      recommendations: ['Deploy across availability zones for high availability.'],
      mitigations: ['Recreate the scale set with zone distribution.'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/virtual-machine-scale-sets-use-availability-zones'],
    },
    azure_vmss_boot_diagnostics_disabled: {
      title: 'VMSS Boot Diagnostics Disabled',
      description: 'Boot diagnostics is not enabled for the VM scale set.',
      serviceName: 'AzureVMSS',
      recommendations: ['Enable boot diagnostics for troubleshooting VM startup issues.'],
      mitigations: ['Enable boot diagnostics in the scale set configuration.'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/boot-diagnostics'],
    },
    azure_vmss_automatic_os_upgrade_disabled: {
      title: 'VMSS Automatic OS Upgrade Disabled',
      description:
        'Automatic OS image upgrades are not enabled for this VM Scale Set. Without automatic upgrades, VMSS instances may run outdated OS images with known vulnerabilities.',
      serviceName: 'AzureVMSS',
      recommendations: ['Enable automatic OS upgrades to keep instances up to date.'],
      mitigations: ['Enable the automatic OS upgrade policy on the scale set.'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/virtual-machine-scale-sets-automatic-upgrade'],
    },
    azure_vmss_manual_upgrade_policy: {
      title: 'VMSS Using Manual Upgrade Policy',
      description: 'The VM scale set uses a manual upgrade policy, requiring manual intervention for updates.',
      serviceName: 'AzureVMSS',
      recommendations: ['Consider using automatic or rolling upgrade policy for smoother updates.'],
      mitigations: ['Update the scale set upgrade policy to Rolling or Automatic.'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/virtual-machine-scale-sets-upgrade-policy'],
    },
    azure_vmss_application_health_extension_missing: {
      title: 'VMSS Application Health Extension Missing',
      description: 'The application health extension is not installed on the VM scale set.',
      serviceName: 'AzureVMSS',
      recommendations: ['Install the application health extension for health monitoring.'],
      mitigations: ['Add the application health extension to the scale set model.'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/virtual-machine-scale-sets-health-extension'],
    },
    azure_policy_assignment_not_enforced: {
      title: 'Azure Policy Assignment Not Enforced',
      description: 'The policy assignment enforcement mode is set to DoNotEnforce.',
      serviceName: 'AzurePolicy',
      recommendations: ['Set the enforcement mode to Default for production environments.'],
      mitigations: ['Update the policy assignment enforcement mode.'],
      references: ['https://learn.microsoft.com/en-us/azure/governance/policy/concepts/assignment-structure'],
    },
    azure_policy_no_assignments: {
      title: 'No Azure Policy Assignments',
      description: 'No policy assignments are configured in the subscription.',
      serviceName: 'AzurePolicy',
      recommendations: ['Create policy assignments to enforce governance standards.'],
      mitigations: ['Assign built-in or custom policies to enforce compliance.'],
      references: ['https://learn.microsoft.com/en-us/azure/governance/policy/overview'],
    },
    azure_arc_machine_disconnected: {
      title: 'Azure Arc Machine Disconnected',
      description: 'The Azure Arc-enabled machine is disconnected from Azure.',
      serviceName: 'AzureArc',
      recommendations: ['Investigate why the machine is disconnected and restore connectivity.'],
      mitigations: ['Check the Azure Connected Machine agent status on the machine.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-arc/servers/troubleshoot-agent-onboard'],
    },
    azure_arc_outdated_agent: {
      title: 'Azure Arc Agent Outdated',
      description: 'The Azure Connected Machine agent is not the latest version.',
      serviceName: 'AzureArc',
      recommendations: ['Update the agent to the latest version.'],
      mitigations: ['Update the agent using the built-in update mechanism.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-arc/servers/manage-agent'],
    },
    azure_arc_machine_stale_status: {
      title: 'Azure Arc Machine Has Stale Status',
      description:
        'The Azure Arc-enabled machine has not reported its status recently. This may indicate network connectivity issues, agent problems, or that the machine has been decommissioned.',
      serviceName: 'AzureArc',
      recommendations: ['Investigate the machine connectivity and agent health.'],
      mitigations: ['Check the machine network connectivity and restart the agent if needed.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-arc/servers/manage-agent'],
    },
    aws_ecr_public_tag_immutable: {
      title: 'ECR Public Repository Image Tags Should Be Immutable',
      description:
        'Amazon ECR Public repository image tags should be configured as immutable to prevent image tags from being overwritten. Immutable tags ensure that each image tag uniquely identifies a specific image version, preventing supply chain attacks through tag manipulation.',
      serviceName: 'AmazonECR',
      recommendations: [
        'Enable image tag immutability for all ECR Public repositories to prevent image tags from being overwritten. This ensures that once an image is pushed with a specific tag, the tag cannot be reassigned to a different image digest.',
      ],
      mitigations: [
        `Update the ECR Public repository to enable image tag immutability:
\`\`\`
aws ecr-public put-repository-catalog-data \\
  --repository-name {{repository_name}} \\
  --region us-east-1
\`\`\`

Alternatively, recreate the repository with immutable tags enabled:
\`\`\`
aws ecr-public create-repository \\
  --repository-name {{repository_name}} \\
  --catalog-data '{}' \\
  --tags Key=Environment,Value=Production
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://docs.aws.amazon.com/AmazonECR/latest/public/public-repository-edit.html'],
    },
    aws_ecs_service_autoscaling_disabled: {
      title: 'ECS Service Auto Scaling Should Be Enabled',
      description:
        'Amazon ECS services should have auto scaling configured to automatically adjust the desired count of tasks in response to demand changes. Without auto scaling, services cannot dynamically respond to traffic patterns, potentially leading to over-provisioning (wasted cost) or under-provisioning (degraded performance).',
      serviceName: 'AmazonECS',
      recommendations: [
        'Configure Application Auto Scaling for the ECS service to automatically adjust task count based on CloudWatch metrics such as CPU utilization, memory utilization, or custom metrics. Set appropriate minimum and maximum capacity limits.',
      ],
      mitigations: [
        `Register the ECS service as a scalable target and create a scaling policy:
\`\`\`
aws application-autoscaling register-scalable-target \\
  --service-namespace ecs \\
  --resource-id service/{{cluster_name}}/{{service_name}} \\
  --scalable-dimension ecs:service:DesiredCount \\
  --min-capacity 2 \\
  --max-capacity 10

aws application-autoscaling put-scaling-policy \\
  --service-namespace ecs \\
  --resource-id service/{{cluster_name}}/{{service_name}} \\
  --scalable-dimension ecs:service:DesiredCount \\
  --policy-name cpu-tracking \\
  --policy-type TargetTrackingScaling \\
  --target-tracking-scaling-policy-configuration '{"TargetValue": 70.0, "PredefinedMetricSpecification": {"PredefinedMetricType": "ECSServiceAverageCPUUtilization"}}'
\`\`\``,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service-auto-scaling.html'],
    },
    azure_activity_log_alert_broad_scope: {
      title: 'Azure Activity Log Alert Has Overly Broad Scope',
      description:
        'The Activity Log Alert is scoped to the entire subscription rather than specific resource groups, which may generate excessive noise.',
      serviceName: 'ActivityLogAlerts',
      recommendations: [
        'The Activity Log Alert is scoped to the entire subscription rather than specific resource groups, which may generate excessive noise.',
      ],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-activity-log'],
    },
    azure_activity_log_alert_disabled: {
      title: 'Azure Activity Log Alert Is Disabled',
      description: 'An Activity Log Alert is disabled and will not trigger notifications for matching events.',
      serviceName: 'ActivityLogAlerts',
      recommendations: ['An Activity Log Alert is disabled and will not trigger notifications for matching events.'],
      mitigations: [
        'Enable Activity Log Alert through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-activity-log'],
    },
    azure_activity_log_alert_empty_condition: {
      title: 'Azure Activity Log Alert Has Empty Condition',
      description: 'The Activity Log Alert has no conditions configured, making it ineffective at filtering events.',
      serviceName: 'ActivityLogAlerts',
      recommendations: ['The Activity Log Alert has no conditions configured, making it ineffective at filtering events.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-activity-log'],
    },
    azure_activity_log_alert_missing_tags: {
      title: 'Azure Activity Log Alert Should Have Tags',
      description: 'The Activity Log Alert has no tags for organization and cost tracking.',
      serviceName: 'ActivityLogAlerts',
      recommendations: ['The Activity Log Alert has no tags for organization and cost tracking.'],
      mitigations: [
        'Configure Activity Log Alert Tags through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['APRA', 'MAS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources'],
    },
    azure_activity_log_alert_no_action_group: {
      title: 'Azure Activity Log Alert Has No Action Group',
      description: 'The Activity Log Alert has no action group configured, so no notifications will be sent when triggered.',
      serviceName: 'ActivityLogAlerts',
      recommendations: ['The Activity Log Alert has no action group configured, so no notifications will be sent when triggered.'],
      mitigations: [
        'Configure Activity Log Alert Action Group through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/action-groups'],
    },
    azure_activity_log_alert_no_conditions: {
      title: 'Azure Activity Log Alert Has No Conditions',
      description: 'The Activity Log Alert has no filter conditions, meaning it will match all events.',
      serviceName: 'ActivityLogAlerts',
      recommendations: ['The Activity Log Alert has no filter conditions, meaning it will match all events.'],
      mitigations: [
        'Configure Activity Log Alert Conditions through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-activity-log'],
    },
    azure_activity_log_alert_no_scopes: {
      title: 'Azure Activity Log Alert Has No Scopes',
      description: 'The Activity Log Alert has no scopes defined, making it inactive.',
      serviceName: 'ActivityLogAlerts',
      recommendations: ['The Activity Log Alert has no scopes defined, making it inactive.'],
      mitigations: [
        'Configure Activity Log Alert Scopes through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-activity-log'],
    },
    azure_appgateway_http2_disabled: {
      title: 'Azure Application Gateway HTTP/2 Should Be Enabled',
      description: 'HTTP/2 is not enabled on the Application Gateway, missing performance improvements.',
      serviceName: 'ApplicationGateway',
      recommendations: ['HTTP/2 is not enabled on the Application Gateway, missing performance improvements.'],
      mitigations: [
        'Enable Application Gateway HTTP/2 through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/application-gateway/configuration-listeners'],
    },
    azure_container_app_low_min_replicas: {
      title: 'Azure Container App Minimum Replicas Should Be Increased',
      description: 'The Container App has very low minimum replicas, which may cause cold start latency during scale-up.',
      serviceName: 'ContainerApps',
      recommendations: ['The Container App has very low minimum replicas, which may cause cold start latency during scale-up.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/container-apps/scale-app'],
    },
    azure_container_app_missing_tags: {
      title: 'Azure Container App Should Have Tags',
      description: 'The Container App has no tags for organization and cost tracking.',
      serviceName: 'ContainerApps',
      recommendations: ['The Container App has no tags for organization and cost tracking.'],
      mitigations: [
        'Configure Container App Tags through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['APRA', 'MAS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources'],
    },
    azure_container_registry_soft_delete_disabled: {
      title: 'Azure Container Registry Soft Delete Should Be Enabled',
      description: 'Soft delete is not enabled, meaning deleted artifacts cannot be recovered.',
      serviceName: 'ContainerRegistry',
      recommendations: ['Soft delete is not enabled, meaning deleted artifacts cannot be recovered.'],
      mitigations: [
        'Enable Container Registry Soft Delete through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/container-registry/container-registry-soft-delete-policy'],
    },
    azure_container_registry_zone_redundancy_disabled: {
      title: 'Azure Container Registry Zone Redundancy Should Be Enabled',
      description: 'Zone redundancy is not enabled, making the registry vulnerable to zonal outages.',
      serviceName: 'ContainerRegistry',
      recommendations: ['Zone redundancy is not enabled, making the registry vulnerable to zonal outages.'],
      mitigations: [
        'Enable Container Registry Zone Redundancy through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/container-registry/zone-redundancy'],
    },
    azure_devops_no_pipelines: {
      title: 'Azure DevOps Project Has No Pipelines',
      description: 'The Azure DevOps project has no CI/CD pipelines configured.',
      serviceName: 'AzureDevOps',
      recommendations: ['The Azure DevOps project has no CI/CD pipelines configured.'],
      mitigations: [
        'Configure DevOps Project Pipelines through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/devops/pipelines/get-started/what-is-azure-pipelines'],
    },
    azure_devops_project_no_description: {
      title: 'Azure DevOps Project Has No Description',
      description: 'The project lacks a description, making it harder for team members to understand its purpose.',
      serviceName: 'AzureDevOps',
      recommendations: ['The project lacks a description, making it harder for team members to understand its purpose.'],
      mitigations: [
        'Configure DevOps Project Description through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/devops/organizations/projects/create-project'],
    },
    azure_devops_repository_disabled: {
      title: 'Azure DevOps Repository Is Disabled',
      description: 'A repository in the project is disabled and not accessible.',
      serviceName: 'AzureDevOps',
      recommendations: ['A repository in the project is disabled and not accessible.'],
      mitigations: [
        'Enable DevOps Repository through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/devops/repos/git/manage-repos'],
    },
    azure_devops_repository_large_size: {
      title: 'Azure DevOps Repository Size Is Very Large',
      description: 'The repository size is unusually large, which may impact clone and fetch performance.',
      serviceName: 'AzureDevOps',
      recommendations: ['The repository size is unusually large, which may impact clone and fetch performance.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/devops/repos/git/manage-large-files'],
    },
    azure_eventgrid_resource_failed_provisioning: {
      title: 'Azure Event Grid Resource Has Failed Provisioning',
      description: 'An Event Grid resource is in a failed provisioning state.',
      serviceName: 'EventGrid',
      recommendations: ['An Event Grid resource is in a failed provisioning state.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/event-grid/troubleshoot-issues'],
    },
    azure_expressroute_enable_global_reach: {
      title: 'Azure ExpressRoute Global Reach Should Be Enabled',
      description: 'Global Reach is not enabled, preventing direct communication between on-premises networks through ExpressRoute.',
      serviceName: 'ExpressRoute',
      recommendations: ['Global Reach is not enabled, preventing direct communication between on-premises networks through ExpressRoute.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/expressroute/expressroute-global-reach'],
    },
    azure_files_smb_not_enabled: {
      title: 'Azure Files SMB Protocol Is Not Enabled',
      description: 'SMB protocol is not enabled on this storage account for Azure Files.',
      serviceName: 'AzureFiles',
      recommendations: ['SMB protocol is not enabled on this storage account for Azure Files.'],
      mitigations: [
        'Enable Files SMB Protocol Is Not Enabled through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/files/storage-files-introduction'],
    },
    azure_firewall_enable_dns_proxy: {
      title: 'Azure Firewall DNS Proxy Should Be Enabled',
      description: 'DNS proxy is not enabled on the firewall, which is required for FQDN filtering.',
      serviceName: 'AzureFirewall',
      recommendations: ['DNS proxy is not enabled on the firewall, which is required for FQDN filtering.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/firewall/dns-settings'],
    },
    azure_frontdoor_profile_no_endpoints: {
      title: 'Azure Front Door Profile Has No Endpoints',
      description: 'The Front Door profile has no endpoints configured, making it non-functional.',
      serviceName: 'FrontDoor',
      recommendations: ['The Front Door profile has no endpoints configured, making it non-functional.'],
      mitigations: [
        'Configure Front Door Profile Endpoints through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/frontdoor/front-door-overview'],
    },
    azure_logic_app_no_actions: {
      title: 'Azure Logic App Has No Actions',
      description: 'The Logic App workflow has no actions defined, making it non-functional.',
      serviceName: 'LogicApps',
      recommendations: ['The Logic App workflow has no actions defined, making it non-functional.'],
      mitigations: [
        'Configure Logic App Actions through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/logic-apps/logic-apps-overview'],
    },
    azure_logic_app_no_triggers: {
      title: 'Azure Logic App Has No Triggers',
      description: 'The Logic App has no triggers configured, so it will never execute.',
      serviceName: 'LogicApps',
      recommendations: ['The Logic App has no triggers configured, so it will never execute.'],
      mitigations: [
        'Configure Logic App Triggers through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/logic-apps/logic-apps-overview'],
    },
    azure_logic_app_workflow_disabled: {
      title: 'Azure Logic App Workflow Is Disabled',
      description: 'The Logic App workflow is disabled and will not execute.',
      serviceName: 'LogicApps',
      recommendations: ['The Logic App workflow is disabled and will not execute.'],
      mitigations: [
        'Enable Logic App Workflow through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/logic-apps/manage-logic-apps-with-azure-portal'],
    },
    azure_metric_alert_auto_mitigation_disabled: {
      title: 'Azure Metric Alert Auto-Mitigation Should Be Enabled',
      description: 'Auto-mitigation is disabled, so the alert will not automatically resolve when the condition clears.',
      serviceName: 'MetricAlerts',
      recommendations: ['Auto-mitigation is disabled, so the alert will not automatically resolve when the condition clears.'],
      mitigations: [
        'Enable Metric Alert Auto-Mitigation through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-metric-overview'],
    },
    azure_metric_alert_broad_scope: {
      title: 'Azure Metric Alert Has Overly Broad Scope',
      description: 'The metric alert is scoped to the entire subscription, which may generate excessive notifications.',
      serviceName: 'MetricAlerts',
      recommendations: ['The metric alert is scoped to the entire subscription, which may generate excessive notifications.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-metric-overview'],
    },
    azure_metric_alert_disabled: {
      title: 'Azure Metric Alert Is Disabled',
      description: 'A metric alert is disabled and will not trigger notifications.',
      serviceName: 'MetricAlerts',
      recommendations: ['A metric alert is disabled and will not trigger notifications.'],
      mitigations: [
        'Enable Metric Alert through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-metric-overview'],
    },
    azure_metric_alert_inefficient_evaluation: {
      title: 'Azure Metric Alert Has Inefficient Evaluation Frequency',
      description: 'The alert evaluation frequency may be too high or too low for the metric type.',
      serviceName: 'MetricAlerts',
      recommendations: ['The alert evaluation frequency may be too high or too low for the metric type.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-metric-overview'],
    },
    azure_metric_alert_missing_tags: {
      title: 'Azure Metric Alert Should Have Tags',
      description:
        'The Azure Monitor metric alert has no tags applied. Tags help organize alerts, track ownership, and enable cost allocation across your Azure environment.',
      serviceName: 'MetricAlerts',
      recommendations: ['The metric alert has no tags for organization.'],
      mitigations: [
        'Configure Metric Alert Tags through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['APRA', 'MAS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources'],
    },
    azure_metric_alert_no_action_group: {
      title: 'Azure Metric Alert Has No Action Group',
      description: 'No action group is configured, so no notifications will be sent.',
      serviceName: 'MetricAlerts',
      recommendations: ['No action group is configured, so no notifications will be sent.'],
      mitigations: [
        'Configure Metric Alert Action Group through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/action-groups'],
    },
    azure_operationalinsights_workspace_low_retention: {
      title: 'Azure Log Analytics Workspace Retention Is Too Low',
      description: 'The Log Analytics workspace retention period is very low, which may impact compliance and troubleshooting capabilities.',
      serviceName: 'LogAnalytics',
      recommendations: ['The Log Analytics workspace retention period is very low, which may impact compliance and troubleshooting capabilities.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/logs/data-retention-configure'],
    },
    azure_pipeline_build_failed: {
      title: 'Azure Pipeline Build Has Failed',
      description:
        'An Azure DevOps pipeline build has failed and requires investigation. Persistent build failures can block deployments and indicate code quality or infrastructure issues.',
      serviceName: 'AzurePipelines',
      recommendations: ['A pipeline build has failed and needs attention.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/devops/pipelines/troubleshooting/troubleshooting'],
    },
    azure_pipeline_build_no_branch: {
      title: 'Azure Pipeline Build Has No Branch Configuration',
      description: 'The pipeline build has no branch configuration specified.',
      serviceName: 'AzurePipelines',
      recommendations: ['The pipeline build has no branch configuration specified.'],
      mitigations: [
        'Configure Pipeline Build Branch Configuration through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/devops/pipelines/repos/azure-repos-git'],
    },
    azure_pipeline_high_failure_rate: {
      title: 'Azure Pipeline Has High Failure Rate',
      description: 'The pipeline has a high failure rate, indicating reliability issues.',
      serviceName: 'AzurePipelines',
      recommendations: ['The pipeline has a high failure rate, indicating reliability issues.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/devops/pipelines/reports/pipelinereport'],
    },
    azure_policy_assignment_no_description: {
      title: 'Azure Policy Assignment Has No Description',
      description: 'The policy assignment lacks a description explaining its purpose.',
      serviceName: 'AzurePolicy',
      recommendations: ['The policy assignment lacks a description explaining its purpose.'],
      mitigations: [
        'Configure Policy Assignment Description through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/governance/policy/overview'],
    },
    azure_policy_custom_definition_no_metadata: {
      title: 'Azure Custom Policy Definition Has No Metadata',
      description: 'The custom policy definition lacks metadata for categorization.',
      serviceName: 'AzurePolicy',
      recommendations: ['The custom policy definition lacks metadata for categorization.'],
      mitigations: [
        'Configure Custom Policy Definition Metadata through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/governance/policy/concepts/definition-structure'],
    },
    azure_policy_definition_no_category: {
      title: 'Azure Policy Definition Has No Category',
      description: 'The policy definition is not categorized, making it harder to organize.',
      serviceName: 'AzurePolicy',
      recommendations: ['The policy definition is not categorized, making it harder to organize.'],
      mitigations: [
        'Configure Policy Definition Category through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/governance/policy/concepts/definition-structure'],
    },
    azure_scheduled_query_rule_auto_mitigation_disabled: {
      title: 'Azure Scheduled Query Rule Auto-Mitigation Disabled',
      description:
        'Auto-mitigation is disabled on this scheduled query alert rule. Without auto-mitigation, alerts will remain active even after the triggering condition resolves.',
      serviceName: 'ScheduledQueryRules',
      recommendations: ['Auto-mitigation is disabled on this alert rule.'],
      mitigations: [
        'Enable Scheduled Query Rule Auto-Mitigation Disabled through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-log'],
    },
    azure_scheduled_query_rule_disabled: {
      title: 'Azure Scheduled Query Rule Is Disabled',
      description:
        'A scheduled query alert rule is disabled and will not trigger notifications. Disabled rules may leave monitoring gaps for the resources they are configured to watch.',
      serviceName: 'ScheduledQueryRules',
      recommendations: ['A scheduled query alert rule is disabled.'],
      mitigations: [
        'Enable Scheduled Query Rule through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-log'],
    },
    azure_scheduled_query_rule_empty_query: {
      title: 'Azure Scheduled Query Rule Has Empty Query',
      description:
        'The scheduled query rule has no KQL query defined. Without a query, the alert rule cannot evaluate conditions and will never trigger.',
      serviceName: 'ScheduledQueryRules',
      recommendations: ['The query rule has no query defined.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-log'],
    },
    azure_scheduled_query_rule_missing_tags: {
      title: 'Azure Scheduled Query Rule Should Have Tags',
      description:
        'The scheduled query alert rule has no tags applied. Tags help organize alerts, track ownership, and enable filtering across your Azure monitoring configuration.',
      serviceName: 'ScheduledQueryRules',
      recommendations: ['The query rule has no tags.'],
      mitigations: [
        'Configure Scheduled Query Rule Tags through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['APRA', 'MAS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources'],
    },
    azure_scheduled_query_rule_no_action_group: {
      title: 'Azure Scheduled Query Rule Has No Action Group',
      description:
        'No action group is configured for this scheduled query rule. Without an action group, triggered alerts will not send notifications to operators.',
      serviceName: 'ScheduledQueryRules',
      recommendations: ['No action group is configured for notifications.'],
      mitigations: [
        'Configure Scheduled Query Rule Action Group through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/action-groups'],
    },
    azure_scheduled_query_rule_no_scopes: {
      title: 'Azure Scheduled Query Rule Has No Scopes',
      description: 'The scheduled query rule has no scopes defined, making it unable to target any resources for evaluation.',
      serviceName: 'ScheduledQueryRules',
      recommendations: ['The query rule has no scopes defined.'],
      mitigations: [
        'Configure Scheduled Query Rule Scopes through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-log'],
    },
    azure_sentinel_incident_no_owner: {
      title: 'Azure Sentinel Incident Has No Owner',
      description: 'A Sentinel security incident has no owner assigned for investigation.',
      serviceName: 'Sentinel',
      recommendations: ['A Sentinel security incident has no owner assigned for investigation.'],
      mitigations: [
        'Configure Sentinel Incident Owner through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/sentinel/investigate-cases'],
    },
    azure_sentinel_stale_incident: {
      title: 'Azure Sentinel Incident Is Stale',
      description: 'A Sentinel incident has been open for an extended period without progress.',
      serviceName: 'Sentinel',
      recommendations: ['A Sentinel incident has been open for an extended period without progress.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/sentinel/investigate-cases'],
    },
    azure_storage_geo_redundant_storage_disabled: {
      title: 'Azure Storage Geo-Redundant Replication Should Be Enabled',
      description:
        'This storage account does not use geo-redundant storage (GRS). Without geo-redundancy, data is only replicated within a single region. A regional outage could result in data loss.',
      serviceName: 'StorageAccounts',
      recommendations: ['Upgrade storage account redundancy to GRS or RA-GRS for cross-region data protection.'],
      mitigations: [
        `Enable geo-redundant storage:
\`\`\`
az storage account update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --sku Standard_GRS
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'APRA'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-redundancy'],
    },
    azure_storage_logging_for_delete_access_disabled: {
      title: 'Azure Storage Delete Access Logging Should Be Enabled',
      description:
        'Storage Analytics logging for delete operations is not enabled. Without delete logging, accidental or malicious data deletion cannot be tracked.',
      serviceName: 'StorageAccounts',
      recommendations: ['Enable Storage Analytics logging for delete operations.'],
      mitigations: [
        `Enable delete logging:
\`\`\`
az storage logging update \\
  --account-name {{resource_name}} \\
  --log d \\
  --retention 90 \\
  --services bqt
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-analytics-logging'],
    },
    azure_storage_logging_for_read_access_disabled: {
      title: 'Azure Storage Read Access Logging Should Be Enabled',
      description:
        'Storage Analytics logging for read operations is not enabled. Without logging, read access to storage resources cannot be audited, making it difficult to detect unauthorized data access.',
      serviceName: 'StorageAccounts',
      recommendations: ['Enable Storage Analytics logging for read operations on blob, queue, and table services.'],
      mitigations: [
        `Enable read logging:
\`\`\`
az storage logging update \\
  --account-name {{resource_name}} \\
  --log r \\
  --retention 90 \\
  --services bqt
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-analytics-logging'],
    },
    azure_storage_logging_for_write_access_disabled: {
      title: 'Azure Storage Write Access Logging Should Be Enabled',
      description:
        'Storage Analytics logging for write operations is not enabled. Without write logging, modifications to storage data cannot be audited.',
      serviceName: 'StorageAccounts',
      recommendations: ['Enable Storage Analytics logging for write operations.'],
      mitigations: [
        `Enable write logging:
\`\`\`
az storage logging update \\
  --account-name {{resource_name}} \\
  --log w \\
  --retention 90 \\
  --services bqt
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-analytics-logging'],
    },
    azure_storage_missing_tags: {
      title: 'Azure Storage Account Should Have Tags',
      description:
        'This storage account has no tags applied. Tags enable cost tracking, resource organization, and governance enforcement across Azure resources.',
      serviceName: 'StorageAccounts',
      recommendations: ['Apply tags to the storage account for cost tracking, ownership identification, and environment classification.'],
      mitigations: [
        `Add tags to the storage account:
\`\`\`
az storage account update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --tags Environment=Production Owner=TeamName CostCenter=12345
\`\`\``,
      ],
      compliances: ['APRA', 'MAS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources'],
    },
    azure_storage_soft_delete_disabled: {
      title: 'Azure Storage Soft Delete Should Be Enabled',
      description:
        'Soft delete is not enabled on this storage account. Without soft delete, accidentally or maliciously deleted data cannot be recovered.',
      serviceName: 'StorageAccounts',
      recommendations: ['Enable blob soft delete and container soft delete with an appropriate retention period.'],
      mitigations: [
        `Enable soft delete:
\`\`\`
az storage account blob-service-properties update \\
  --account-name {{resource_name}} \\
  --resource-group {{resource_group}} \\
  --enable-delete-retention true \\
  --delete-retention-days 30 \\
  --enable-container-delete-retention true \\
  --container-delete-retention-days 30
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/blobs/soft-delete-blob-overview'],
    },
    azure_storage_versioning_disabled: {
      title: 'Azure Storage Blob Versioning Should Be Enabled',
      description:
        'Blob versioning is not enabled. Without versioning, previous versions of blobs are lost when overwritten, making it impossible to recover from accidental overwrites or corruption.',
      serviceName: 'StorageAccounts',
      recommendations: ['Enable blob versioning to maintain previous versions of blobs for data protection and recovery.'],
      mitigations: [
        `Enable blob versioning:
\`\`\`
az storage account blob-service-properties update \\
  --account-name {{resource_name}} \\
  --resource-group {{resource_group}} \\
  --enable-versioning true
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/blobs/versioning-overview'],
    },
    azure_vm_accelerated_networking_disabled: {
      title: 'Azure VM Accelerated Networking Should Be Enabled',
      description:
        'Accelerated Networking enables single root I/O virtualization (SR-IOV) to a VM, greatly improving networking performance. This high-performance path bypasses the host from the datapath, reducing latency, jitter, and CPU utilization.',
      serviceName: 'VirtualMachines',
      recommendations: [
        "Enable Accelerated Networking on the VM's network interface. This is supported on most general-purpose and compute-optimized VM sizes with 2 or more vCPUs.",
      ],
      mitigations: [
        `Enable accelerated networking (requires VM to be deallocated):
\`\`\`
az vm deallocate \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}

az network nic update \\
  --resource-group {{resource_group}} \\
  --name {{nic_name}} \\
  --accelerated-networking true

az vm start \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}
\`\`\``,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/accelerated-networking-overview'],
    },
    azure_vm_auto_shutdown_disabled: {
      title: 'Azure VM Auto-Shutdown Should Be Configured',
      description:
        'Auto-shutdown is not configured for this VM. For non-production workloads (dev/test environments), auto-shutdown can significantly reduce costs by automatically deallocating VMs during off-hours when they are not needed.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Configure auto-shutdown for non-production VMs to automatically deallocate them during off-hours. Set appropriate notification settings to alert users before shutdown.',
      ],
      mitigations: [
        `Configure auto-shutdown for the VM:
\`\`\`
az vm auto-shutdown \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --time 1900 \\
  --email {{notification_email}}
\`\`\``,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/auto-shutdown-vm'],
    },
    azure_vm_automatic_os_upgrade_disabled: {
      title: 'Azure VM Automatic OS Upgrade Should Be Enabled',
      description:
        'Automatic OS upgrades help keep VMs secure by automatically applying the latest OS patches. Without automatic upgrades, VMs may run with known vulnerabilities, increasing the attack surface.',
      serviceName: 'VirtualMachines',
      recommendations: ['Enable automatic OS image upgrades for the VM to ensure timely application of security patches and updates.'],
      mitigations: [
        `Enable automatic OS upgrades via Azure Policy or during VM creation. For existing VMs, update the OS upgrade policy:
\`\`\`
az vm update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --set 'osProfile.windowsConfiguration.enableAutomaticUpdates=true'
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/automatic-vm-guest-patching'],
    },
    azure_vm_backup_disabled: {
      title: 'Azure VM Backup Should Be Enabled',
      description:
        'Azure Virtual Machine backup is not configured. Azure Backup provides independent and isolated backups to guard against unintended data destruction. Without backup, data loss from accidental deletion, corruption, or ransomware cannot be recovered.',
      serviceName: 'VirtualMachines',
      recommendations: [
        "Enable Azure Backup for this VM using a Recovery Services vault with an appropriate backup policy. Configure retention policies that meet your organization's RPO and RTO requirements.",
      ],
      mitigations: [
        `Enable backup for the VM using Azure CLI:
\`\`\`
az backup protection enable-for-vm \\
  --resource-group {{resource_group}} \\
  --vault-name {{vault_name}} \\
  --vm {{resource_name}} \\
  --policy-name DefaultPolicy
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/backup/backup-azure-vms-first-look-arm'],
    },
    azure_vm_boot_diagnostics_disabled: {
      title: 'Azure VM Boot Diagnostics Should Be Enabled',
      description:
        'Boot diagnostics is a debugging feature for Azure VMs that captures serial console output and screenshots to help diagnose VM startup issues. Without boot diagnostics, troubleshooting boot failures becomes significantly more difficult.',
      serviceName: 'VirtualMachines',
      recommendations: ['Enable boot diagnostics for this VM to capture serial console output and VM screenshots for troubleshooting purposes.'],
      mitigations: [
        `Enable boot diagnostics with managed storage:
\`\`\`
az vm boot-diagnostics enable \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/boot-diagnostics'],
    },
    azure_vm_guest_level_diagnostics_missing: {
      title: 'Azure VM Guest-Level Diagnostics Should Be Enabled',
      description:
        'Guest-level diagnostics collects OS-level metrics (memory, disk I/O, process info) from within the VM. Without guest diagnostics, you can only see host-level metrics and miss critical insights for troubleshooting and capacity planning.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Enable guest-level diagnostics by installing the Azure Diagnostics extension to collect OS-level performance counters and logs.',
      ],
      mitigations: [
        `Enable diagnostics using the Azure Monitor Agent:
\`\`\`
az vm extension set \\
  --resource-group {{resource_group}} \\
  --vm-name {{resource_name}} \\
  --name AzureMonitorLinuxAgent \\
  --publisher Microsoft.Azure.Monitor
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/agents/azure-monitor-agent-overview'],
    },
    azure_vm_monitor_agent_missing: {
      title: 'Azure VM Monitor Agent Should Be Installed',
      description:
        'The Azure Monitor Agent is not installed on this VM. Without the monitoring agent, Azure Monitor cannot collect guest OS metrics, logs, and traces, limiting observability and alerting capabilities.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Install the Azure Monitor Agent to enable collection of guest OS metrics and logs for monitoring, alerting, and diagnostics.',
      ],
      mitigations: [
        `Install the Azure Monitor Agent:
\`\`\`
az vm extension set \\
  --resource-group {{resource_group}} \\
  --vm-name {{resource_name}} \\
  --name AzureMonitorLinuxAgent \\
  --publisher Microsoft.Azure.Monitor
\`\`\`

For Windows VMs:
\`\`\`
az vm extension set \\
  --resource-group {{resource_group}} \\
  --vm-name {{resource_name}} \\
  --name AzureMonitorWindowsAgent \\
  --publisher Microsoft.Azure.Monitor
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-monitor/agents/azure-monitor-agent-manage'],
    },
    azure_vm_unmanaged_disk: {
      title: 'Azure VM Should Use Managed Disks',
      description:
        'This VM is using unmanaged disks (VHD files in storage accounts). Managed disks provide better reliability, scalability, and security features including built-in encryption, RBAC support, and snapshot capabilities.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Migrate from unmanaged disks to Azure Managed Disks for improved reliability, simplified management, and enhanced security features.',
      ],
      mitigations: [
        `Convert the VM to use managed disks:
\`\`\`
az vm deallocate \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}

az vm convert \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}

az vm start \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/managed-disks-overview'],
    },
    azure_vmss_accelerated_networking_disabled: {
      title: 'Azure VMSS Accelerated Networking Should Be Enabled',
      description:
        'Accelerated Networking is not enabled on this VM Scale Set. Enabling it provides lower latency, reduced jitter, and decreased CPU utilization for network-intensive workloads.',
      serviceName: 'VirtualMachineScaleSets',
      recommendations: ['Enable Accelerated Networking on the VMSS network interface configuration for improved network performance.'],
      mitigations: [
        `Update the VMSS network configuration:
\`\`\`
az vmss update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --set virtualMachineProfile.networkProfile.networkInterfaceConfigurations[0].enableAcceleratedNetworking=true
\`\`\``,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/accelerated-networking-overview'],
    },
    azure_vmss_overprovision_disabled: {
      title: 'Azure VMSS Overprovisioning Should Be Enabled',
      description:
        'Overprovisioning is disabled on this VM Scale Set. With overprovisioning enabled, Azure provisions more VMs than requested during scale-out, then deletes the extras. This reduces deployment failures and improves scale-out speed at no additional cost.',
      serviceName: 'VirtualMachineScaleSets',
      recommendations: ['Enable overprovisioning to improve scale-out reliability. Azure does not charge for the extra provisioned VMs.'],
      mitigations: [
        `Enable overprovisioning:
\`\`\`
az vmss update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --set overprovision=true
\`\`\``,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/virtual-machine-scale-sets-design-overview'],
    },
    azure_vnet_address_space_overprovisioned: {
      title: 'Azure VNet Address Space Is Over-Provisioned',
      description: 'The VNet has a very large address space that may not be necessary.',
      serviceName: 'VirtualNetworks',
      recommendations: ['The VNet has a very large address space that may not be necessary.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/virtual-networks-overview'],
    },
    azure_vnet_custom_dns_not_configured: {
      title: 'Azure VNet Custom DNS Is Not Configured',
      description: 'The VNet uses default Azure DNS instead of custom DNS servers.',
      serviceName: 'VirtualNetworks',
      recommendations: ['The VNet uses default Azure DNS instead of custom DNS servers.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/virtual-networks-name-resolution-for-vms-and-role-instances'],
    },
    azure_vnet_empty_no_subnets: {
      title: 'Azure VNet Has No Subnets',
      description: 'The VNet has no subnets configured, making it non-functional.',
      serviceName: 'VirtualNetworks',
      recommendations: ['The VNet has no subnets configured, making it non-functional.'],
      mitigations: [
        'Configure VNet Subnets through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/virtual-network-manage-subnet'],
    },
    azure_vnet_gateway_transit_not_optimized: {
      title: 'Azure VNet Gateway Transit Is Not Optimized',
      description: 'VNet peering gateway transit is not optimally configured.',
      serviceName: 'VirtualNetworks',
      recommendations: ['VNet peering gateway transit is not optimally configured.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/virtual-network-peering-overview'],
    },
    missing_tags: {
      title: 'Missing Tags/Labels',
      description:
        'This resource does not have proper tags or labels configured. Tags help with cost allocation, access control, and resource organization.',
      recommendations: ['Add appropriate tags or labels to this resource for better organization, cost tracking, and access management.'],
    },
    storage_public_access: {
      title: 'Storage Public Access Enabled',
      description: 'This storage resource has public access enabled, which could expose sensitive data to unauthorized users.',
      recommendations: ['Review the public access configuration and restrict access to only authorized users and applications.'],
    },
    storage_versioning_disabled: {
      title: 'Storage Versioning Disabled',
      description:
        'Object versioning is not enabled for this storage resource. Versioning provides data protection against accidental deletion or overwrites.',
      recommendations: ['Enable versioning to protect against accidental data deletion or modification.'],
    },
    storage_no_lifecycle: {
      title: 'Storage Lifecycle Not Configured',
      description:
        'This storage resource does not have lifecycle management rules configured. Lifecycle policies help optimize costs by automatically transitioning or expiring objects.',
      recommendations: ['Configure lifecycle rules to automatically manage object storage classes and expiration based on access patterns.'],
    },
    storage_no_cmek: {
      title: 'Storage Not Using Customer-Managed Key',
      description:
        'This storage resource is not encrypted with a customer-managed encryption key (CMEK). Using CMEK provides additional control over data encryption.',
      recommendations: ['Configure customer-managed encryption keys for enhanced data security and compliance.'],
    },
    db_backup_disabled: {
      title: 'Database Backup Disabled',
      description:
        'Automated backups are not properly configured for this database instance. Regular backups are essential for data protection and disaster recovery.',
      recommendations: ['Enable automated backups with an appropriate retention period to ensure data protection.'],
    },
    db_public_access: {
      title: 'Database Public Access Enabled',
      description: 'This database instance has public network access enabled, which increases the attack surface and risk of unauthorized access.',
      recommendations: ['Disable public network access and use private endpoints or VPN for database connectivity.'],
    },
    db_storage_autoscaling: {
      title: 'Database Storage Autoscaling Disabled',
      description:
        'Storage autoscaling is not enabled for this database instance. Without autoscaling, the database may run out of storage during peak usage.',
      recommendations: ['Enable storage autoscaling to ensure the database can handle storage growth automatically.'],
    },
    k8s_logging_disabled: {
      title: 'Kubernetes Logging Disabled',
      description:
        'Cluster logging is not enabled for this Kubernetes cluster. Logging is essential for security monitoring, troubleshooting, and compliance.',
      recommendations: ['Enable cluster logging to capture API server, audit, and controller manager logs for security and operational visibility.'],
    },
    k8s_network_policy: {
      title: 'Kubernetes Network Policy Disabled',
      description:
        'Network policies are not enforced in this Kubernetes cluster. Without network policies, all pods can communicate freely, increasing security risk.',
      recommendations: [
        'Enable network policy enforcement and define policies to restrict pod-to-pod communication based on the principle of least privilege.',
      ],
    },
  },
  Security: {
    aws_lambda_function_url: {
      title: 'Check Lambda Function URL Not in Use',
      description:
        'A function URL is a dedicated HTTP(S) endpoint created for your Amazon Lambda function. You can use a function URL to invoke your Lambda function through a browser, curl, Postman, or an HTTP client. However, a function URL should be used with caution, and should only be applied on functions with relevant and secure access control, otherwise you risk exposing your application to the public.',
      serviceName: 'AWSLambda',
      recommendations: [
        'Check whether your Amazon Lambda functions are configured with function URLs for HTTP(S) endpoints. A function URL creates a direct HTTP(S) endpoint to your function and this may pose a security risk depending on the security configuration and intention of the function.',
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        aws lambda delete-function-url-config --region {{region}} --function-name {{resource_id}}
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/lambda/latest/dg/lambda-urls.html'],
    },
    aws_elasticache_encryption: {
      title: 'At Rest Encryption Should be enabled for ElastiCache Clusters',
      description:
        'When working with production and confidential data it is strongly recommended to implement encryption in order to protect your data from unauthorized access and fulfill compliance requirements for data-at-rest and in-transit encryption within your organization. For example, a compliance requirement is to protect sensitive data that could potentially identify a specific individual such as Personally Identifiable Information (PII), usually used in Financial Services, Healthcare, and Telecommunications sectors.',
      serviceName: 'AmazonElastiCache',
      recommendations: [
        `Ensure that your Amazon ElastiCache Redis cache clusters are encrypted in order to meet security and compliance requirements. Encryption helps prevent unauthorized users from reading sensitive data available on your Redis cache clusters and their associated cache storage systems. This includes data saved to persistent media, known as data at-rest, and data that can be intercepted as it travels through the network, between clients and cache servers, known as data in-transit.`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
            terraform {
            required_providers {
                aws = {
                source  = "hashicorp/aws"
                version = "~> 4.0"
                }
            }

            required_version = ">= 0.14.9"
            }

            provider "aws" {
            region  = "us-east-1"
            }

            resource "aws_elasticache_replication_group" "redis-cache-cluster" {

            replication_group_id        = "cc-encrypted-redis-cache-cluster"
            description                 = "Encrypted Redis Cache Replication Group"
            engine                      = "redis"
            engine_version              = "6.x"
            node_type                   = "cache.t2.micro"
            num_cache_clusters          = 2
            parameter_group_name        = "default.redis6.x"

            # Enable In-Transit and At-Rest Encryption
            at_rest_encryption_enabled  = true
            kms_key_id                  = "arn:aws:kms:us-east-1:123456789012:key/abcd1234-abcd-1234-abcd-1234abcd1234"

            }
\`\`\`
`,
        `
        AWS CLI command to enable In-Transit Encryption for ElastiCache cluster:
\`\`\`aws elasticache create-replication-group
                --region {{region}}
                --replication-group-id "cc-encrypted-redis-cache-cluster"
                --replication-group-description "Encrypted Redis Cache Replication Group" --engine "redis"
                --num-cache-clusters 2
                --cache-node-type "cache.t2.micro"
                --transit-encryption-enabled
                --at-rest-encryption-enabled
                --kms-key-id arn:aws:kms:us-east-1:123456789012:key/abcd1234-abcd-1234-abcd-1234abcd1234
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/at-rest-encryption.html'],
    },
    aws_elasticache_encryption_intransit: {
      title: 'At Transit Encryption Should be enabled for ElastiCache Clusters',
      description:
        'When working with production and confidential data it is strongly recommended to implement encryption in order to protect your data from unauthorized access and fulfill compliance requirements for data-at-rest and in-transit encryption within your organization. For example, a compliance requirement is to protect sensitive data that could potentially identify a specific individual such as Personally Identifiable Information (PII), usually used in Financial Services, Healthcare, and Telecommunications sectors.',
      serviceName: 'AmazonElastiCache',
      recommendations: [
        `Ensure that your Amazon ElastiCache Redis cache clusters are encrypted in order to meet security and compliance requirements. Encryption helps prevent unauthorized users from reading sensitive data available on your Redis cache clusters and their associated cache storage systems. This includes data saved to persistent media, known as data at-rest, and data that can be intercepted as it travels through the network, between clients and cache servers, known as data in-transit.`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
            terraform {
            required_providers {
                aws = {
                source  = "hashicorp/aws"
                version = "~> 4.0"
                }
            }

            required_version = ">= 0.14.9"
            }

            provider "aws" {
            region  = "us-east-1"
            }

            resource "aws_elasticache_replication_group" "redis-cache-cluster" {

            replication_group_id        = "cc-encrypted-redis-cache-cluster"
            description                 = "Encrypted Redis Cache Replication Group"
            engine                      = "redis"
            engine_version              = "6.x"
            node_type                   = "cache.t2.micro"
            num_cache_clusters          = 2
            parameter_group_name        = "default.redis6.x"

            # Enable In-Transit and At-Rest Encryption
            at_rest_encryption_enabled  = true
            kms_key_id                  = "arn:aws:kms:us-east-1:123456789012:key/abcd1234-abcd-1234-abcd-1234abcd1234"

            }
\`\`\`
`,
        `AWS CLI command to enable In-Transit Encryption for ElastiCache cluster:
\`\`\`aws elasticache create-replication-group
                --region {{region}}
                --replication-group-id "cc-encrypted-redis-cache-cluster"
                --replication-group-description "Encrypted Redis Cache Replication Group" --engine "redis"
                --num-cache-clusters 2
                --cache-node-type "cache.t2.micro"
                --transit-encryption-enabled
                --at-rest-encryption-enabled
                --kms-key-id arn:aws:kms:us-east-1:123456789012:key/abcd1234-abcd-1234-abcd-1234abcd1234
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/in-transit-encryption.html'],
    },
    aws_ec2_ebs_encrypt: {
      title: 'Ec2 EBS Volumes Should be Encrypted',
      description:
        'When working with production and confidential data it is strongly recommended to implement encryption in order to protect your data from unauthorized access and fulfill compliance requirements for data-at-rest and in-transit encryption within your organization. For example, a compliance requirement is to protect sensitive data that could potentially identify a specific individual such as Personally Identifiable Information (PII), usually used in Financial Services, Healthcare, and Telecommunications sectors.',
      serviceName: 'AmazonEC2',
      recommendations: [
        `Ensure that your EBS Volumes are encrypted in order to meet security and compliance requirements. Encryption helps prevent unauthorized users from reading sensitive data available on your Ec2 Instance. This includes data saved to persistent media, known as data at-rest, and data that can be intercepted as it travels through the network, between clients and cache servers, known as data in-transit.`,
      ],
      mitigations: [
        `**By Default Enable Encryption for EBS Volumes**
\`\`\`
        aws ec2 enable-ebs-encryption-by-default
\`\`\`
`,
        `**Enable Encryption for Existing EBS Volumes**
        1. Create a snapshot of the EBS volume
        2. Copy snapshot (unencrypted) to an encrypted copy using AWS Managed Key
        3. Create a new EBS volume from the encrypted snapshot in the same Availability Zone as your EC2 instance
        4. Attach the new (encrypted) volume to the Amazon EC2 instance on a different device
        5. Restart the encrypted EC2 instance
`,
      ],
      references: ['https://docs.aws.amazon.com/ebs/latest/userguide/ebs-encryption.html'],
    },
    aws_ec2_instance_public_ip: {
      title: 'Disable Public IP Address Assignment for EC2 Instances',
      description:
        'Amazon EC2 instances should not get public IP addresses at launch in order to enhance security by reducing the attack surface. Instead, they should be placed in private VPC subnets and accessed through the associated load balancer. This setup ensures that incoming traffic is tightly controlled and monitored.',
      serviceName: 'AmazonEC2',
      recommendations: [
        `Ensure that Amazon EC2 instances such as backend instances are not using public IP addresses in order to prevent Internet exposure. Backend instances are EC2 instances that run behind a load balancer and do not need direct access to the Internet, therefore do not require public IP addresses.`,
      ],
      mitigations: [
        `**Launch Command Without Public IP Address**
\`\`\`
        aws ec2 run-instances
            --region {{region}}
            --image-id ami-0abcdabcdabcdabcd
            --count 1
            --instance-type t2.micro
            --key-name admin-ssh-key
            --security-group-ids sg-01234abcd1234abcd
            --subnet-id subnet-0abcd1234abcd1234
            --no-associate-public-ip-address
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-instance-addressing.html'],
    },
    aws_ec2_instance_public_subnet: {
      title: 'EC2 Instances Should Not Be Launched in Public Subnets',
      description:
        'By provisioning Amazon EC2 instances within a private subnet (logically isolated section of VPC) you will prevent your instances from receiving inbound traffic initiated by someone on the Internet, therefore have the guarantee that no malicious requests can reach your backend instances.',
      serviceName: 'AmazonEC2',
      recommendations: [
        `Ensure that no backend Amazon EC2 instances are provisioned in public subnets in order to protect them from exposure to the Internet. Backend instances are EC2 instances that do not require direct access to the public internet such as database, API, or caching servers. To follow cloud security best practices, all Amazon EC2 instances that are not Internet-facing should run within a private subnet, behind a NAT gateway that allows downloading software updates and implementing security patches or accessing other AWS services like SQS and SNS.`,
      ],
      mitigations: [
        `To move your backend Amazon EC2 instances from public subnets to private subnets, you must re-create these instances within private VPC subnets.
         - Run create-image command (OSX/Linux/UNIX) to create an image from the source, non-compliant backend EC2 instance. Include the --no-reboot command parameter to guarantee the file system integrity for your new AMI
         - Execute run-instances command (OSX/Linux/UNIX) to launch a new backend Amazon EC2 instance from the AMI created at the previous steps. Use the information returned at step no. 2 for the instance configuration parameters. Configure the --subnet-id command parameter with the ID of your private VPC subnet and include the --no-associate-public-ip-address parameter in the command request to avoid assigning automatically a public IPv4 address to the new EC2 instance         
\`\`\`
            aws ec2 run-instances
            --region {{region}}
            --image-id ami-0abcdabcdabcdabcd
            --count 1
            --instance-type t2.micro
            --key-name conformity
            --security-group-ids sg-01234abcd1234abcd
            --iam-instance-profile Name="ec2-manager-role"
            --subnet-id subnet-abcdabcd
            --no-associate-public-ip-address         
\`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-instance-addressing.html'],
    },
    aws_ec2_instance_generation_upgrade: {
      title: 'Ec2 Instance generation Upgrade',
      description:
        'Using the current (latest) generation of EC2 instance types instead of the previous generation has multiple advantages such as better hardware performance (faster CPUs, increased memory and network throughput), better virtualization technology (HVM), and lower costs.',
      serviceName: 'AmazonEC2',
      recommendations: [
        `Ensure that all your Amazon EC2 instances are using the latest generation of instance types in order to get the best performance with lower costs.`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        terraform {
            required_providers {
                aws = {
                    source  = "hashicorp/aws"
                    version = "~> 3.27"
                }
            }

            required_version = ">= 0.14.9"
        }

        provider "aws" {
            profile = "default"
            region  = "us-east-1"
        }

        resource "aws_instance" "new-generation-instance" {

            ami = "ami-0abcd1234abcd1234"
            instance_type = "c5.large"

            lifecycle {
                ignore_changes = [ami]
            }

        }
\`\`\`
`,
        `AWS CLI command to upgrade EC2 instance generation:

        - Run stop-instances command (OSX/Linux/UNIX) to stop the Amazon EC2 instance that you want to reconfigure:
\`\`\`
        aws ec2 stop-instances --region {{region}} --instance-ids {{recommendation.instance_id}}
\`\`\`

        - Run modify-instance-attribute command (OSX/Linux/UNIX) to change the instance type of the stopped Amazon EC2 instance to the latest generation:
\`\`\`
        aws ec2 modify-instance-attribute --region {{region}} --instance-id {{recommendation.instance_id}} --instance-type '{"Value": "{{recommendation.recommended_instance_type}}"}'
\`\`\`

        - Run start-instances command (OSX/Linux/UNIX) to start the Amazon EC2 instance with the new instance type:
\`\`\`
        aws ec2 start-instances --region {{region}} --instance-ids {{recommendation.instance_id}}
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instance-types.html'],
    },
    aws_ecr_pushscan_enabled: {
      title: 'Enable Scan on Push for ECR Container Images',
      description:
        'For the security and compliance status of your applications it is crucial to detect and respond to Amazon ECR container image vulnerabilities in the early stages of deployment. When Scan on Push security feature is enabled, your container images are automatically scanned after being pushed to your Amazon ECR repository. If Scan on Push is disabled on your repository, then each image scan must be manually started to get scan results.',
      serviceName: 'AmazonECR',
      recommendations: [
        `Ensure that all your Amazon ECR container images are automatically scanned for security vulnerabilities and expenses after being pushed to a repository. Scan on Push for Amazon ECR is an automated vulnerability assessment feature that helps you improve the security of your ECR container images by scanning them for a broad range of Operating System (OS) vulnerabilities after being pushed to an ECR repository. The security feature uses the Common Vulnerabilities and Exposures (CVEs) database from Clair, an open source project designed for static analysis of security issues in appc and docker containers.`,
      ],
      mitigations: [
        `**AWS CLI command to enable Scan on Push for ECR repository:**
\`\`\`
            aws ecr put-image-scanning-configuration
            --region {{region}}
            --repository-name cc-docker-web-repo
            --image-scanning-configuration scanOnPush=true
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonECR/latest/userguide/encryption-at-rest.html'],
    },
    aws_eks_public_access: {
      title: 'EKS Cluster Endpoint Public Access',
      description: `When launching a cluster on Amazon EKS, an endpoint is automatically generated for the Kubernetes API server. This endpoint allows you to interact with your newly created cluster. By default, this API server endpoint is publicly accessible, meaning any machine on the internet can potentially connect to your EKS cluster using its public endpoint. This exposes your cluster to a higher risk of malicious activities and attacks. Restricting public access to the Kubernetes API endpoint managed by the EKS cluster is a security best practice that helps protect your cluster from unauthorized access and potential security threats. By not allowing public access to the cluster's Kubernetes API endpoint, you ensure that only authorized entities can interact with your Amazon EKS cluster.`,
      serviceName: 'AmazonEKS',
      recommendations: [
        `Ensure that your Amazon EKS cluster's Kubernetes API server endpoint is not publicly accessible from the Internet in order to avoid exposing private data and minimizing security risks. The level of access to your Kubernetes API server endpoints depends on your EKS application use cases, however, for most use cases Cloud Conformity recommends that the API server endpoints should be accessible only from within your AWS Virtual Private Cloud (VPC).`,
      ],
      mitigations: [
        `***AWS CLI command to restrict public access to EKS cluster endpoint:***
\`\`\`
        aws eks update-cluster-config
        --region {{region}}
        --name cc-eks-webapp-cluster
        --resources-vpc-config endpointPublicAccess=false,endpointPrivateAccess=true,publicAccessCidrs=["10.0.0.20/32"]
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/eks/latest/userguide/cluster-endpoint.html'],
    },
    aws_eks_secret_encryption: {
      title: 'Enable Envelope Encryption for EKS Kubernetes Secrets',
      description:
        'When working with security-critical data, it is strongly recommended to enable encryption of Kubernetes secrets in order to protect your data from unauthorized access and fulfill compliance requirements for data-at-rest encryption within your organization. For example, a compliance requirement is to protect sensitive data that could potentially identify a specific individual such as Personally Identifiable Information (PII), usually used for financial processing systems and healthcare services.',
      serviceName: 'AmazonEKS',
      recommendations: [
        `Use AWS Key Management Service (KMS) keys to provide envelope encryption of Kubernetes secrets stored in Amazon Elastic Kubernetes Service (EKS), in order to meet security and compliance requirements. Implementing envelope encryption of Kubernetes secrets is considered a security best practice for applications that store sensitive and confidential data. Set up your own AWS KMS Customer Master Key (CMK) and associate the key with your Amazon EKS cluster. When secrets are stored using the Kubernetes secrets API, they are encrypted with a Kubernetes-generated data encryption key, which is then further encrypted using the associated KMS CMK that you have created.`,
      ],
      mitigations: [
        `**AWS CLI command to enable Envelope Encryption for EKS Kubernetes Secrets:**
\`\`\`
        aws eks create-cluster
        --region {{region}}
        --name cc-new-prod-cluster
        --role-arn arn:aws:iam::123456789012:role/cc-eks-role
        --resources-vpc-config subnetIds=subnet-1234abcd,subnet-abcd1234
        --encryption-config resources=secrets,provider={keyArn=arn:aws:kms:us-east-1:123456789012:key/abcdabcd-1234-abcd-1234-abcd1234abcd}
        --query 'cluster.encryptionConfig'
\`\`\`

**Enable For Existing Cluster:**
\`\`\`
        aws eks associate-encryption-config \
        --cluster-name {{resource_name}} \
        --encryption-config '[{"resources":["secrets"],"provider":{"keyArn":"arn:aws:kms:region-code:account:key/key"}}]'
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/eks/latest/userguide/enable-kms.html'],
    },
    aws_lambda_environment_variable_encryption: {
      title: 'Enable Encryption in Transit for Environment Variables',
      description:
        'When dealing with Lambda function environment variables that hold sensitive and critical data, it is highly recommended to implement encryption in order to protect the data that you dynamically pass to your functions (usually access information) from unauthorized access.',
      serviceName: 'AWSLambda',
      recommendations: [
        `Ensure that all Amazon Lambda function environment variables that store sensitive information such as passwords, tokens and access keys are encrypted in order to meet security and compliance requirements. The environment variables defined for your Lambda functions are key-value pairs that are used to store configuration settings without the need to change function code. By default, all Lambda environment variables with the key (name) set to "pass", "password", "*token*" (i.e. any key that has "token" string in it), "api", "API", "Key", "KEY", "key" are encrypted`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        resource "aws_lambda_function" "lambda-function" {
            function_name    = "cc-app-worker-function"
            s3_bucket        = "cc-lambda-functions"
            s3_key           = "worker.zip" 
            role             = aws_iam_role.lambda-execution-role.arn
            handler          = "lambda_function.lambda_handler"
            runtime          = "python3.9"
            memory_size      = 1024
            timeout          = 45   
            vpc_config {
                subnet_ids         = [ "subnet-01234abcd1234abcd", "subnet-0abcd1234abcd1234" ]
                security_group_ids = [ "sg-0abcd1234abcd1234" ]
            }
            tracing_config {
                mode = "Active"
            }
            environment {
                variables = {
                    DatabaseName = "lambda-db-name"
                    DatabaseUser = "lambda-db-user"
                }
                kms_key_arn = "arn:aws:kms:us-east-1:123456789012:key/1234abcd-1234-abcd-1234-abcd1234abcd"
            }
        }
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/lambda/latest/dg/configuration-envvars.html'],
    },
    aws_rds_public_access: {
      title: 'RDS Should Not Be Publicly Accessible',
      description:
        'When the security group associated with an Amazon RDS database instance allows unrestricted access (i.e. 0.0.0.0/0), everyone and everything on the Internet can establish a connection to your database instance and this can increase the opportunity for malicious activities such as brute-force attacks, SQL injection or DDoS attacks.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `Check for any public-facing Amazon RDS database instances provisioned within your AWS cloud account and restrict unauthorized access in order to minimize security risks. To restrict access to a publicly accessible database instance, you must disable the PubliclyAccessible configuration flag, and update the security group associated with the database instance.`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
            resource "aws_db_instance" "rds-database-instance" {
                allocated_storage      = 20
                engine                 = "mysql"
                engine_version         = "5.7"
                instance_class         = "db.t2.micro"
                name                   = "mysqldb"
                username               = "ccmysqluser01"
                password               = "ccmysqluserpwd"
                parameter_group_name   = "default.mysql5.7"
                vpc_security_group_ids = [ aws_security_group.db-security-group.id ]

                # Restrict Public Access for RDS Database Instances
                publicly_accessible = false


                apply_immediately = true
            }
\`\`\`
`,
        `**AWS CLI command to disable Public Access for RDS instance:**
\`\`\`
        aws rds modify-db-instance
            --region {{region}}
            --db-instance-identifier {{recommendation.instance_id}}
            --no-publicly-accessible
            --apply-immediately
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/CHAP_CommonTasks.Connect.html'],
    },
    aws_rds_storage_encrypted: {
      title: 'RDS Encryption Enabled',
      description:
        'When dealing with production databases that hold sensitive and critical data, it is highly recommended to implement encryption in order to protect your data from unauthorized access. With Amazon RDS encryption enabled, the data stored on the instance underlying storage, the automated backups, Read Replicas, and snapshots, become all encrypted. The RDS encryption keys implement AES-256 algorithm and are entirely managed and protected by the AWS key management infrastructure through Amazon Key Management Service (KMS).',
      serviceName: 'AmazonRDS',
      recommendations: [
        `Ensure that your Amazon RDS database instances are encrypted to fulfill compliance requirements for data-at-rest encryption. The data encryption and decryption process is handled transparently and does not require any additional action from you or your application.`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        resource "aws_db_instance" "rds-database-instance" {
            allocated_storage         = 20
            engine                    = "mysql"
            engine_version            = "5.7"
            instance_class            = "db.t2.small"
            name                      = "mysqldb"
            username                  = "ccmysqluser01"
            password                  = "ccmysqluserpwd"
            parameter_group_name      = "default.mysql5.7"
            final_snapshot_identifier = "rds-database-instance-snapshot"

            # Enable Encryption at Rest
            storage_encrypted = true


            apply_immediately = true
        }
\`\`\`
`,
        `**AWS CLI command to enable Encryption at Rest for RDS instance:**
\`\`\`
        aws rds create-db-snapshot
        --region {{region}}
        --db-snapshot-identifier {{recommendation.instance_id}}-snapshot
        --db-instance-identifier {{recommendation.instance_id}}
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Overview.Encryption.html'],
    },
    aws_rds_instance_public_subnet: {
      title: 'RDS Instance Should Not Be Launched in Public Subnets',
      description:
        'By provisioning your Amazon RDS instances within private subnets (logically isolated sections of AWS VPC), you will prevent these resources from receiving inbound traffic from the public Internet, therefore you can have the guarantee that no malicious requests can reach your database instances from the Internet.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `Ensure that no Amazon RDS database instances are provisioned inside VPC public subnets in order to protect them from direct exposure to the Internet. Because database instances are not Internet-facing and their management (running software updates, implementing security patches, etc) is performed by AWS, the database instances should run only in private subnets.`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
            resource "aws_db_instance" "rds-database-instance" {
                allocated_storage      = 50
                engine                 = "mysql"
                engine_version         = "5.7"
                instance_class         = "db.t3.medium"
                name                   = "[database-name]"
                username               = "[master-username]"
                password               = "[master-password]"
                parameter_group_name   = "default.mysql5.7"
                vpc_security_group_ids = ["sg-0123456789abcdefa"]
                publicly_accessible    = false
                
                # Database Instance Not in Public Subnet
                db_subnet_group_name   = aws_db_subnet_group.db-subnet-group.name
            }        
\`\`\`
`,
        `**AWS CLI command to move RDS instance from public to private subnet:**
\`\`\`
        aws rds modify-db-instance
            --region {{region}}
            --db-instance-identifier {{recommendation.instance_id}}
            --db-subnet-group-name cc-private-db-subnet-group
            --apply-immediately
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_VPC.WorkingWithRDSInstanceinaVPC.html'],
    },
    aws_rds_snapshot_encryption: {
      title: 'RDS snapshots should be encrypted',
      description:
        'When working with production databases that hold sensitive and critical data, it is strongly recommended to implement encryption at rest and protect your data from attackers or unauthorized personnel.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `Ensure that your manual Amazon RDS database snapshots are encrypted in order to achieve compliance for data-at-rest encryption within your organization. The Amazon RDS snapshot encryption and decryption process is handled transparently and does not require any additional action from you or your application. The keys used for database snapshot encryption can be entirely managed and protected by the AWS key management infrastructure or fully managed by the AWS customer through Amazon KMS Customer Master Keys (CMKs).`,
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        resource "aws_db_snapshot" "rds-db-instance-snapshot" {
            db_instance_identifier = aws_db_instance.rds-database-instance.id
            db_snapshot_identifier = "cc-db-instance-snapshot"
            encrypted              = true
            kms_key_id             = "aws_kms_key.kms-key.arn"
        }   
\`\`\`
`,
        `**AWS CLI command to enable Encryption for RDS snapshot:**
\`\`\`
            aws rds copy-db-snapshot
            --region {{region}}
            --source-db-snapshot-identifier {{recommendation.instance_id}}-snapshot
            --target-db-snapshot-identifier cc-encrypted-project5-mysql-database-feb-2021
            --kms-key-id arn:aws:kms:<aws-region>:<aws-account-id>:alias/aws/rds
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Overview.Encryption.html'],
    },
    aws_s3_public_access_acl: {
      title: "S3 Bucket Public 'READ_ACP' Access",
      description:
        'Granting public READ_ACP access to your Amazon S3 buckets can enable the unauthorized users to see who controls your S3 objects and how. Malicious users can use this information to find S3 objects with misconfigured permissions and try probing techniques to facilitate access to your Amazon S3 data. To meet security and compliance requirements, avoid granting READ_ACP (VIEW) permissions to the "Everyone (public access)" grantee in production.',
      serviceName: 'AmazonS3',
      recommendations: [
        "Ensure that the content permissions of your Amazon S3 buckets can't be viewed by anonymous users in order to protect your S3 data against unauthorized access. An Amazon S3 bucket that grants public READ_ACP (VIEW) access can allow everyone on the Internet to examine your Access Control List (ACL) configuration and find permission vulnerabilities.",
      ],
      mitigations: [
        `**AWS CLI command to remove public READ_ACP access from S3 bucket:**
\`\`\`
        aws s3api put-bucket-acl
        --bucket {{recommendation.bucket_name}}
        --acl private
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonS3/latest/userguide/access-control-block-public-access.html'],
    },
    aws_s3_public_access_policy: {
      title: 'S3 Bucket Public Access Via Policy',
      description:
        'Granting public access to your Amazon S3 buckets via bucket policies can allow malicious users to view, get, upload, modify, and delete S3 objects, which can lead to data breaches, data loss and unexpected charges on your AWS monthly bill.',
      serviceName: 'AmazonS3',
      recommendations: [
        `Ensure that your Amazon S3 buckets are not publicly accessible to the Internet via bucket policies in order to protect against unauthorized access. Allowing unrestricted access through bucket policies gives everyone the ability to list the objects within the bucket (ListBucket), download objects (GetObject), upload/delete objects (PutObject, DeleteObject), view objects permissions (GetBucketAcl), edit objects permissions (PutBucketAcl) and more. Trend Micro Cloud One™ – Conformity strongly recommends using bucket policies to limit the access to a trusted entity, such as an authorized AWS account, instead of providing access to everyone on the Internet.`,
      ],
      mitigations: [
        `**AWS CLI command to remove public access via bucket policy:**
\`\`\`
        aws s3api put-bucket-policy
        --bucket {{recommendation.bucket_name}}
        --policy "{}"
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonS3/latest/userguide/access-control-block-public-access.html'],
    },
    // --- AWS Fargate Security ---
    aws_fargate_task_definition_secrets_not_used: {
      title: 'Fargate Task Definition Not Using Secrets',
      description: 'The container has environment variables with sensitive values instead of using secrets from Secrets Manager or SSM.',
      serviceName: 'AWSFargate',
      recommendations: ['Use AWS Secrets Manager or SSM Parameter Store for sensitive values instead of environment variables.'],
      mitigations: ['Update the container definition to use `secrets` field instead of hardcoded environment variables.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/specifying-sensitive-data.html'],
    },
    aws_fargate_task_definition_privileged_container: {
      title: 'Fargate Task Definition Running Privileged Container',
      description: 'The container is configured to run in privileged mode, which provides unrestricted host access.',
      serviceName: 'AWSFargate',
      recommendations: ['Disable privileged mode unless absolutely necessary.'],
      mitigations: ['Set `privileged` to false in the container definition.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html'],
    },
    aws_fargate_task_definition_readonly_root_fs_disabled: {
      title: 'Fargate Task Definition Read-Only Root Filesystem Disabled',
      description: 'The container root filesystem is writable, increasing the attack surface.',
      serviceName: 'AWSFargate',
      recommendations: ['Enable read-only root filesystem and use volumes for writable paths.'],
      mitigations: ['Set `readonlyRootFilesystem` to true in the container definition.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html'],
    },
    // --- AWS Redshift Security ---
    aws_redshift_encryption_at_rest: {
      title: 'Redshift Encryption at Rest Should Be Enabled',
      description: 'The Redshift cluster does not have encryption at rest enabled.',
      serviceName: 'AmazonRedshift',
      recommendations: ['Enable encryption at rest for the Redshift cluster to protect data.'],
      mitigations: [
        `
**Enable encryption via:**
\`\`\`
aws redshift modify-cluster --cluster-identifier {{recommendation.cluster_id}} --encrypted
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/redshift/latest/mgmt/working-with-db-encryption.html'],
    },
    aws_redshift_public_access: {
      title: 'Redshift Cluster Should Not Be Publicly Accessible',
      description: 'The Redshift cluster is publicly accessible, exposing it to potential unauthorized access.',
      serviceName: 'AmazonRedshift',
      recommendations: ['Disable public accessibility for the Redshift cluster.'],
      mitigations: [
        `
\`\`\`
aws redshift modify-cluster --cluster-identifier {{recommendation.cluster_id}} --no-publicly-accessible
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/redshift/latest/mgmt/managing-clusters-vpc.html'],
    },
    aws_redshift_enhanced_vpc_routing: {
      title: 'Redshift Enhanced VPC Routing Should Be Enabled',
      description: 'Enhanced VPC routing forces all COPY and UNLOAD traffic to go through the VPC.',
      serviceName: 'AmazonRedshift',
      recommendations: ['Enable enhanced VPC routing to keep data traffic within the VPC.'],
      mitigations: [
        `
\`\`\`
aws redshift modify-cluster --cluster-identifier {{recommendation.cluster_id}} --enhanced-vpc-routing
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/redshift/latest/mgmt/enhanced-vpc-routing.html'],
    },
    // --- AWS Elasticsearch Security ---
    aws_es_encryption_at_rest: {
      title: 'Elasticsearch Encryption at Rest Should Be Enabled',
      description: 'The Elasticsearch domain does not have encryption at rest enabled.',
      serviceName: 'AmazonES',
      recommendations: ['Enable encryption at rest to protect stored data.'],
      mitigations: ['Update the domain configuration to enable encryption at rest.'],
      references: ['https://docs.aws.amazon.com/opensearch-service/latest/developerguide/encryption-at-rest.html'],
    },
    aws_es_node_to_node_encryption: {
      title: 'Elasticsearch Node-to-Node Encryption Should Be Enabled',
      description: 'Node-to-node encryption is not enabled for the Elasticsearch domain.',
      serviceName: 'AmazonES',
      recommendations: ['Enable node-to-node encryption to protect data in transit within the cluster.'],
      mitigations: ['Update the domain configuration to enable node-to-node encryption.'],
      references: ['https://docs.aws.amazon.com/opensearch-service/latest/developerguide/ntn.html'],
    },
    // --- AWS EC2 Security ---
    aws_ec2_instance_imds_token_optional: {
      title: 'EC2 Instance IMDSv2 Not Required',
      description: 'The instance metadata service allows IMDSv1 which is vulnerable to SSRF attacks.',
      serviceName: 'AmazonEC2',
      recommendations: ['Require IMDSv2 (token-based) to protect against SSRF attacks.'],
      mitigations: [
        `
\`\`\`
aws ec2 modify-instance-metadata-options --instance-id i-xxx --http-tokens required
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/configuring-instance-metadata-service.html'],
    },
    aws_ec2_stopped_instance_incurring_storage_cost: {
      title: 'Stopped EC2 Instance Still Incurring Storage Costs',
      description: 'The EC2 instance is stopped but its EBS volumes continue to incur charges.',
      serviceName: 'AmazonEC2',
      recommendations: ['Delete the instance and its volumes if no longer needed, or create snapshots and terminate.'],
      mitigations: ['Terminate the instance or detach and delete unnecessary volumes.'],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Stop_Start.html'],
    },
    // --- AWS MSK Security ---
    aws_msk_encryption_in_transit: {
      title: 'MSK Encryption in Transit Should Be Enabled',
      description: 'Data in transit between MSK brokers is not encrypted.',
      serviceName: 'AmazonMSK',
      recommendations: ['Enable TLS encryption for data in transit between brokers and clients.'],
      mitigations: ['Update the cluster encryption settings to enable in-transit encryption.'],
      references: ['https://docs.aws.amazon.com/msk/latest/developerguide/msk-encryption.html'],
    },
    aws_msk_encryption_at_rest: {
      title: 'MSK Encryption at Rest Should Be Enabled',
      description: 'Data at rest is not encrypted with a CMK for the MSK cluster.',
      serviceName: 'AmazonMSK',
      recommendations: ['Enable encryption at rest using a CMK for the MSK cluster.'],
      mitigations: ['Encryption at rest can only be set at cluster creation time.'],
      references: ['https://docs.aws.amazon.com/msk/latest/developerguide/msk-encryption.html'],
    },
    aws_msk_public_access_disabled: {
      title: 'MSK Public Access Should Be Disabled',
      description: 'Public access is enabled for the MSK cluster, exposing it to the internet.',
      serviceName: 'AmazonMSK',
      recommendations: ['Disable public access to the MSK cluster.'],
      mitigations: ['Update the cluster connectivity settings to disable public access.'],
      references: ['https://docs.aws.amazon.com/msk/latest/developerguide/public-access.html'],
    },
    // --- AWS CloudTrail Security ---
    aws_cloudtrail_encryption_cmk: {
      title: 'CloudTrail Should Use CMK Encryption',
      description: 'CloudTrail logs are not encrypted with a customer managed KMS key.',
      serviceName: 'AWSCloudTrail',
      recommendations: ['Encrypt CloudTrail logs with a CMK for enhanced security.'],
      mitigations: [
        `
\`\`\`
aws cloudtrail update-trail --name {{resource_name}} --kms-key-id arn:aws:kms:...:key/...
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/awscloudtrail/latest/userguide/encrypting-cloudtrail-log-files-with-aws-kms.html'],
    },
    aws_cloudtrail_eds_encryption_cmk: {
      title: 'CloudTrail Event Data Store Should Use CMK Encryption',
      description: 'The CloudTrail event data store is not encrypted with a CMK.',
      serviceName: 'AWSCloudTrail',
      recommendations: ['Encrypt the event data store with a CMK.'],
      mitigations: ['Update the event data store encryption settings.'],
      references: ['https://docs.aws.amazon.com/awscloudtrail/latest/userguide/encrypting-cloudtrail-log-files-with-aws-kms.html'],
    },
    // --- AWS ECR Public ---
    aws_ecrpublic_tag_immutable: {
      title: 'ECR Public Image Tags Should Be Immutable',
      description: 'The ECR Public repository does not have image tag immutability enabled.',
      serviceName: 'AmazonECR',
      recommendations: ['Enable image tag immutability to prevent image tags from being overwritten.'],
      mitigations: ['Update the repository to enable image tag immutability.'],
      references: ['https://docs.aws.amazon.com/AmazonECR/latest/userguide/image-tag-mutability.html'],
    },
    // --- AWS DynamoDB Security ---
    aws_dynamodb_sse_cmk: {
      title: 'DynamoDB Should Use CMK Encryption',
      description: 'The DynamoDB table is not encrypted with a customer managed KMS key.',
      serviceName: 'AmazonDynamoDB',
      recommendations: ['Use CMK encryption for DynamoDB tables storing sensitive data.'],
      mitigations: ['Update the table SSE specification to use a CMK.'],
      references: ['https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/EncryptionAtRest.html'],
    },
    // --- AWS CloudWatch Security ---
    aws_cloudwatch_log_group_encryption_cmk: {
      title: 'CloudWatch Log Group Should Use CMK Encryption',
      description: 'The log group is not encrypted with a customer managed KMS key.',
      serviceName: 'AmazonCloudWatch',
      recommendations: ['Encrypt CloudWatch log groups with a CMK for enhanced security.'],
      mitigations: [
        `
\`\`\`
aws logs associate-kms-key --log-group-name {{recommendation.log_group_name}} --kms-key-id arn:aws:kms:...:key/...
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/encrypt-log-data-kms.html'],
    },
    // --- AWS Backup Security ---
    aws_backup_vault_encryption_cmk: {
      title: 'Backup Vault Should Use CMK Encryption',
      description: 'The backup vault is not encrypted with a customer managed KMS key.',
      serviceName: 'AWSBackup',
      recommendations: ['Use CMK encryption for backup vaults storing sensitive data.'],
      mitigations: ['Create a new vault with CMK encryption (cannot be changed after creation).'],
      references: ['https://docs.aws.amazon.com/aws-backup/latest/devguide/encryption.html'],
    },
    aws_backup_vault_access_policy_exists: {
      title: 'Backup Vault Should Have an Access Policy',
      description: 'The backup vault does not have a resource policy to control access.',
      serviceName: 'AWSBackup',
      recommendations: ['Configure a resource policy on the backup vault.'],
      mitigations: ['Set a vault access policy to control who can access the backups.'],
      references: ['https://docs.aws.amazon.com/aws-backup/latest/devguide/vaults.html'],
    },
    aws_backup_vault_lock_enabled: {
      title: 'Backup Vault Lock Should Be Enabled',
      description: 'Vault Lock is not enabled, so backup policies can be modified or deleted.',
      serviceName: 'AWSBackup',
      recommendations: ['Enable Vault Lock for compliance and immutability requirements.'],
      mitigations: ['Put a vault lock configuration on the backup vault.'],
      references: ['https://docs.aws.amazon.com/aws-backup/latest/devguide/vault-lock.html'],
    },
    // --- AWS CloudFront Security ---
    aws_cloudfront_waf_integration: {
      title: 'CloudFront Should Have WAF Integration',
      description: 'The CloudFront distribution does not have a WAF web ACL associated.',
      serviceName: 'AmazonCloudFront',
      recommendations: ['Associate a WAF web ACL with the distribution to protect against web exploits.'],
      mitigations: ['Associate a WAF web ACL with the CloudFront distribution.'],
      references: ['https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/distribution-web-awswaf.html'],
    },
    aws_cloudfront_viewer_protocol_https: {
      title: 'CloudFront Should Enforce HTTPS',
      description: 'The distribution allows HTTP connections, which transmit data unencrypted.',
      serviceName: 'AmazonCloudFront',
      recommendations: ['Configure the distribution to redirect HTTP to HTTPS or require HTTPS only.'],
      mitigations: ['Update the distribution behavior to use `redirect-to-https` or `https-only`.'],
      references: ['https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/using-https-viewers-to-cloudfront.html'],
    },
    aws_cloudfront_origin_access_control: {
      title: 'CloudFront Should Use Origin Access Control',
      description: 'The distribution does not use Origin Access Control (OAC) to restrict S3 access.',
      serviceName: 'AmazonCloudFront',
      recommendations: ['Use Origin Access Control to ensure S3 content is only accessible through CloudFront.'],
      mitigations: ['Create an OAC and associate it with the distribution origin.'],
      references: ['https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-restricting-access-to-s3.html'],
    },
    // --- AWS VPC Security ---
    aws_vpc_unallocated_elastic_ip: {
      title: 'Unallocated Elastic IP Address',
      description: 'The Elastic IP address is not associated with any instance or network interface, incurring unnecessary costs.',
      serviceName: 'AmazonVPC',
      recommendations: ['Release unused Elastic IPs to avoid unnecessary charges.'],
      mitigations: [
        `
\`\`\`
aws ec2 release-address --allocation-id eipalloc-xxx
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/elastic-ip-addresses-eip.html'],
    },
    // --- AWS X-Ray Security ---
    aws_xray_encryption_cmk: {
      title: 'X-Ray Should Use CMK Encryption',
      description: 'X-Ray trace data is not encrypted with a customer managed KMS key.',
      serviceName: 'AWSX-Ray',
      recommendations: ['Encrypt X-Ray data with a CMK for enhanced security.'],
      mitigations: ['Update the X-Ray encryption configuration to use a CMK.'],
      references: ['https://docs.aws.amazon.com/xray/latest/devguide/xray-console-encryption.html'],
    },
    // --- AWS ECS Security ---
    aws_ecs_cluster_fargate_fips_disabled: {
      title: 'ECS Fargate FIPS Compliance Disabled',
      description: 'FIPS 140-2 compliance is not enabled for the Fargate capacity provider.',
      serviceName: 'AmazonECS',
      recommendations: ['Enable FIPS compliance if required by regulatory standards.'],
      mitigations: ['Update the cluster Fargate capacity provider to enable FIPS.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs-fips-compliance.html'],
    },
    aws_ecs_task_definition_secrets_not_used: {
      title: 'ECS Task Definition Not Using Secrets',
      description: 'The container uses hardcoded environment variables instead of secrets references.',
      serviceName: 'AmazonECS',
      recommendations: ['Use secrets from Secrets Manager or SSM Parameter Store.'],
      mitigations: ['Update the container definition to use the `secrets` field.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/specifying-sensitive-data.html'],
    },
    aws_ecs_task_definition_privileged_container: {
      title: 'ECS Task Definition Running Privileged Container',
      description:
        'The ECS task definition runs containers in privileged mode, granting full access to the host system. Privileged containers bypass security boundaries and increase the blast radius of container compromise.',
      serviceName: 'AmazonECS',
      recommendations: ['Disable privileged mode unless absolutely necessary.'],
      mitigations: ['Set `privileged` to false in the container definition.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html'],
    },
    aws_ecs_task_definition_readonly_root_fs_disabled: {
      title: 'ECS Task Definition Read-Only Root Filesystem Disabled',
      description:
        'The ECS task definition does not use a read-only root filesystem. A writable root filesystem allows attackers to modify binaries or install malware if a container is compromised.',
      serviceName: 'AmazonECS',
      recommendations: ['Enable read-only root filesystem.'],
      mitigations: ['Set `readonlyRootFilesystem` to true.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html'],
    },
    // --- AWS EFS Security ---
    aws_efs_encryption_at_rest: {
      title: 'EFS Encryption at Rest Should Be Enabled',
      description: 'The EFS file system does not have encryption at rest enabled.',
      serviceName: 'AmazonEFS',
      recommendations: ['Enable encryption at rest for EFS file systems.'],
      mitigations: ['Encryption can only be enabled at creation time. Create a new encrypted file system and migrate data.'],
      references: ['https://docs.aws.amazon.com/efs/latest/ug/encryption-at-rest.html'],
    },
    // --- AWS KMS Security ---
    aws_kms_key_rotation_enabled: {
      title: 'KMS Key Rotation Should Be Enabled',
      description: 'Automatic key rotation is not enabled for the KMS key.',
      serviceName: 'AWSKMS',
      recommendations: ['Enable automatic key rotation for KMS keys.'],
      mitigations: [
        `
\`\`\`
aws kms enable-key-rotation --key-id KEY_ID
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/kms/latest/developerguide/rotate-keys.html'],
    },
    // --- AWS SNS Security ---
    aws_sns_sse_enabled_cmk: {
      title: 'SNS Topic Should Use CMK Encryption',
      description: 'The SNS topic is not encrypted with a customer managed KMS key.',
      serviceName: 'AmazonSNS',
      recommendations: ['Encrypt SNS topics with a CMK.'],
      mitigations: ['Set the `KmsMasterKeyId` attribute on the SNS topic.'],
      references: ['https://docs.aws.amazon.com/sns/latest/dg/sns-server-side-encryption.html'],
    },
    aws_sns_topic_no_public_access: {
      title: 'SNS Topic Should Not Allow Public Access',
      description:
        'The SNS topic resource policy allows public access, enabling any AWS account or anonymous user to publish or subscribe. This can lead to data exfiltration or unauthorized message injection.',
      serviceName: 'AmazonSNS',
      recommendations: ['Restrict the SNS topic policy to authorized principals only.'],
      mitigations: ['Update the topic policy to remove public access.'],
      references: ['https://docs.aws.amazon.com/sns/latest/dg/sns-security-best-practices.html'],
    },
    // --- AWS SQS Security ---
    aws_sqs_sse_enabled: {
      title: 'SQS Server-Side Encryption Should Be Enabled',
      description: 'The SQS queue does not have server-side encryption enabled.',
      serviceName: 'AmazonSQS',
      recommendations: ['Enable SSE for SQS queues.'],
      mitigations: ['Set the `SqsManagedSseEnabled` or `KmsMasterKeyId` attribute on the queue.'],
      references: ['https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-server-side-encryption.html'],
    },
    // --- AWS SageMaker Security ---
    aws_sagemaker_notebook_no_direct_internet: {
      title: 'SageMaker Notebook Should Not Have Direct Internet Access',
      description: 'The SageMaker notebook instance has direct internet access enabled.',
      serviceName: 'AmazonSageMaker',
      recommendations: ['Disable direct internet access and use VPC endpoints for connectivity.'],
      mitigations: ['Recreate the notebook with `DirectInternetAccess` set to Disabled.'],
      references: ['https://docs.aws.amazon.com/sagemaker/latest/dg/appendix-notebook-and-internet-access.html'],
    },
    aws_sagemaker_notebook_root_access_disabled: {
      title: 'SageMaker Notebook Root Access Should Be Disabled',
      description: 'Root access is enabled for the SageMaker notebook instance.',
      serviceName: 'AmazonSageMaker',
      recommendations: ['Disable root access to reduce the attack surface.'],
      mitigations: ['Update the notebook instance to disable root access.'],
      references: ['https://docs.aws.amazon.com/sagemaker/latest/dg/nbi-root-access.html'],
    },
    aws_sagemaker_notebook_encryption_cmk: {
      title: 'SageMaker Notebook Should Use CMK Encryption',
      description: 'The notebook instance is not encrypted with a customer managed KMS key.',
      serviceName: 'AmazonSageMaker',
      recommendations: ['Use CMK encryption for SageMaker notebook instances.'],
      mitigations: ['Recreate the notebook with a KMS key specified.'],
      references: ['https://docs.aws.amazon.com/sagemaker/latest/dg/encryption-at-rest.html'],
    },
    aws_sagemaker_endpoint_data_capture: {
      title: 'SageMaker Endpoint Data Capture Should Be Enabled',
      description: 'Data capture is not enabled for the SageMaker endpoint.',
      serviceName: 'AmazonSageMaker',
      recommendations: ['Enable data capture for model monitoring and auditing.'],
      mitigations: ['Update the endpoint configuration to enable data capture.'],
      references: ['https://docs.aws.amazon.com/sagemaker/latest/dg/model-monitor-data-capture.html'],
    },
    // --- AWS Bedrock Security ---
    aws_bedrock_custom_model_output_encryption_cmk: {
      title: 'Bedrock Custom Model Should Use CMK Encryption',
      description: 'The custom model output is not encrypted with a CMK.',
      serviceName: 'AmazonBedrock',
      recommendations: ['Use CMK encryption for Bedrock custom model outputs.'],
      mitigations: ['Specify a KMS key when creating the custom model.'],
      references: ['https://docs.aws.amazon.com/bedrock/latest/userguide/encryption-customer-managed-keys.html'],
    },
    // --- AWS Inspector Security ---
    aws_inspector_not_enabled: {
      title: 'AWS Inspector Not Enabled',
      description: 'Amazon Inspector is not enabled in this account/region.',
      serviceName: 'AmazonInspector',
      recommendations: ['Enable Inspector for automated vulnerability management.'],
      mitigations: ['Enable Inspector via the console or CLI.'],
      references: ['https://docs.aws.amazon.com/inspector/latest/user/getting_started_tutorial.html'],
    },
    aws_inspector_ec2_not_enabled: {
      title: 'Inspector EC2 Scanning Not Enabled',
      description: 'EC2 instance scanning is not enabled in Inspector.',
      serviceName: 'AmazonInspector',
      recommendations: ['Enable EC2 scanning to detect vulnerabilities in instances.'],
      mitigations: ['Enable EC2 scanning in the Inspector configuration.'],
      references: ['https://docs.aws.amazon.com/inspector/latest/user/enable-disable-scanning-ec2.html'],
    },
    aws_inspector_ecr_not_enabled: {
      title: 'Inspector ECR Scanning Not Enabled',
      description: 'ECR container image scanning is not enabled in Inspector.',
      serviceName: 'AmazonInspector',
      recommendations: ['Enable ECR scanning to detect vulnerabilities in container images.'],
      mitigations: ['Enable ECR scanning in the Inspector configuration.'],
      references: ['https://docs.aws.amazon.com/inspector/latest/user/enable-disable-scanning-ecr.html'],
    },
    aws_inspector_lambda_not_enabled: {
      title: 'Inspector Lambda Scanning Not Enabled',
      description: 'Lambda function scanning is not enabled in Inspector.',
      serviceName: 'AmazonInspector',
      recommendations: ['Enable Lambda scanning to detect vulnerabilities in function code.'],
      mitigations: ['Enable Lambda scanning in the Inspector configuration.'],
      references: ['https://docs.aws.amazon.com/inspector/latest/user/enable-disable-scanning-lambda.html'],
    },
    aws_inspector_critical_finding: {
      title: 'Inspector Critical Finding',
      description: 'A critical vulnerability has been found by Amazon Inspector.',
      serviceName: 'AmazonInspector',
      recommendations: ['Address critical findings immediately to reduce security risk.'],
      mitigations: ['Apply patches or updates to remediate the vulnerability.'],
      references: ['https://docs.aws.amazon.com/inspector/latest/user/findings-understanding.html'],
    },
    aws_inspector_old_finding: {
      title: 'Inspector Finding Is Old',
      description: 'An Inspector finding has been open for an extended period without remediation.',
      serviceName: 'AmazonInspector',
      recommendations: ['Review and remediate old findings or suppress if accepted risk.'],
      mitigations: ['Apply remediation or update the finding status.'],
      references: ['https://docs.aws.amazon.com/inspector/latest/user/findings-understanding.html'],
    },
    aws_inspector_no_coverage: {
      title: 'Inspector Has No Coverage',
      description:
        'No EC2 instances, ECR repositories, or Lambda functions are being scanned by Amazon Inspector. Without coverage, vulnerabilities in your workloads go undetected.',
      serviceName: 'AmazonInspector',
      recommendations: ['Ensure Inspector is scanning your resources.'],
      mitigations: ['Enable resource types for scanning in Inspector.'],
      references: ['https://docs.aws.amazon.com/inspector/latest/user/monitoring_coverage.html'],
    },
    // --- AWS Secrets Manager Security ---
    aws_secretsmanager_unused_secret: {
      title: 'Secrets Manager Secret Is Unused',
      description: 'The secret has not been accessed recently, suggesting it may be unused.',
      serviceName: 'AWSSecretsManager',
      recommendations: ['Review and delete unused secrets to reduce the attack surface.'],
      mitigations: ['Delete the secret if confirmed unused.'],
      references: ['https://docs.aws.amazon.com/secretsmanager/latest/userguide/manage_delete-secret.html'],
    },
    aws_secretsmanager_rotation_enabled: {
      title: 'Secrets Manager Rotation Should Be Enabled',
      description:
        'Automatic rotation is not enabled for this Secrets Manager secret. Long-lived secrets increase the risk of credential compromise. Enable automatic rotation with an appropriate rotation schedule.',
      serviceName: 'AWSSecretsManager',
      recommendations: ['Enable automatic rotation for secrets to enhance security.'],
      mitigations: ['Configure rotation with a Lambda function.'],
      references: ['https://docs.aws.amazon.com/secretsmanager/latest/userguide/rotating-secrets.html'],
    },
    aws_secretsmanager_encryption_cmk: {
      title: 'Secrets Manager Should Use CMK Encryption',
      description: 'The secret is not encrypted with a customer managed KMS key.',
      serviceName: 'AWSSecretsManager',
      recommendations: ['Use CMK encryption for secrets containing sensitive data.'],
      mitigations: ['Update the secret to use a CMK for encryption.'],
      references: ['https://docs.aws.amazon.com/secretsmanager/latest/userguide/security-encryption.html'],
    },
    // --- AWS SSM Security ---
    aws_ssm_parameter_not_encrypted: {
      title: 'SSM Parameter Not Encrypted',
      description: 'The SSM parameter is stored as plaintext instead of SecureString.',
      serviceName: 'AWSSystemsManager',
      recommendations: ['Use SecureString type for parameters containing sensitive data.'],
      mitigations: ['Recreate the parameter as SecureString type with KMS encryption.'],
      references: ['https://docs.aws.amazon.com/systems-manager/latest/userguide/sysman-paramstore-securestring.html'],
    },
    // --- AWS WAF Security ---
    aws_waf_logging_disabled: {
      title: 'WAF Logging Disabled',
      description:
        'Logging is not enabled for the AWS WAF web ACL. Without logging, blocked and allowed requests cannot be audited, making it difficult to tune rules and investigate security incidents.',
      serviceName: 'AWSWAF',
      recommendations: ['Enable logging to capture web ACL traffic for analysis.'],
      mitigations: ['Enable logging with a Kinesis Data Firehose, S3, or CloudWatch Logs destination.'],
      references: ['https://docs.aws.amazon.com/waf/latest/developerguide/logging.html'],
    },
    aws_waf_webacl_not_associated: {
      title: 'WAF Web ACL Not Associated with Resources',
      description: 'The WAF web ACL is not associated with any resources.',
      serviceName: 'AWSWAF',
      recommendations: ['Associate the web ACL with CloudFront, ALB, or API Gateway resources.'],
      mitigations: ['Associate the web ACL with the appropriate resource.'],
      references: ['https://docs.aws.amazon.com/waf/latest/developerguide/web-acl-associating-aws-resource.html'],
    },
    aws_waf_no_rules: {
      title: 'WAF Web ACL Has No Rules',
      description: 'The WAF web ACL does not have any rules configured.',
      serviceName: 'AWSWAF',
      recommendations: ['Add rules to the web ACL to protect against common web exploits.'],
      mitigations: ['Add managed rule groups or custom rules to the web ACL.'],
      references: ['https://docs.aws.amazon.com/waf/latest/developerguide/web-acl-rules.html'],
    },
    aws_waf_no_rate_limiting: {
      title: 'WAF Has No Rate Limiting',
      description: 'The WAF web ACL does not include rate-based rules.',
      serviceName: 'AWSWAF',
      recommendations: ['Add rate-based rules to protect against DDoS and brute force attacks.'],
      mitigations: ['Add a rate-based rule to the web ACL.'],
      references: ['https://docs.aws.amazon.com/waf/latest/developerguide/waf-rule-statement-type-rate-based.html'],
    },
    aws_waf_no_managed_rules: {
      title: 'WAF Has No AWS Managed Rules',
      description: 'The WAF web ACL does not use any AWS managed rule groups.',
      serviceName: 'AWSWAF',
      recommendations: ['Add AWS managed rule groups for protection against common vulnerabilities.'],
      mitigations: ['Add the AWS managed rules for common threats (e.g., AWSManagedRulesCommonRuleSet).'],
      references: ['https://docs.aws.amazon.com/waf/latest/developerguide/aws-managed-rule-groups.html'],
    },
    aws_waf_empty_ipset: {
      title: 'WAF IP Set Is Empty',
      description: 'A WAF IP set has no addresses, which may indicate misconfiguration.',
      serviceName: 'AWSWAF',
      recommendations: ['Review the IP set and add addresses or remove if unused.'],
      mitigations: ['Add IP addresses to the set or delete it if not needed.'],
      references: ['https://docs.aws.amazon.com/waf/latest/developerguide/waf-ip-set-managing.html'],
    },
    // --- GCP Security ---
    gcp_function_public_access: {
      title: 'Cloud Function Allows Public Access',
      description: 'The Cloud Function is accessible without authentication.',
      serviceName: 'CloudFunctions',
      recommendations: ['Restrict access to authenticated users only unless public access is intended.'],
      mitigations: ['Remove the allUsers or allAuthenticatedUsers IAM binding from the function.'],
      references: ['https://cloud.google.com/functions/docs/securing/managing-access-iam'],
    },
    gcp_bigquery_dataset_no_cmek: {
      title: 'BigQuery Dataset Not Using CMEK',
      description: 'The BigQuery dataset is not encrypted with a customer-managed encryption key.',
      serviceName: 'BigQuery',
      recommendations: ['Use CMEK for datasets containing sensitive data.'],
      mitigations: ['Set the default encryption configuration on the dataset.'],
      references: ['https://cloud.google.com/bigquery/docs/customer-managed-encryption'],
    },
    gcp_run_public_ingress: {
      title: 'Cloud Run Service Allows Public Ingress',
      description: 'The Cloud Run service allows traffic from the public internet.',
      serviceName: 'CloudRun',
      recommendations: ['Restrict ingress to internal traffic only if public access is not required.'],
      mitigations: ['Update the service ingress setting to internal or internal-and-cloud-load-balancing.'],
      references: ['https://cloud.google.com/run/docs/securing/ingress'],
    },
    gcp_sql_no_ssl: {
      title: 'Cloud SQL SSL Not Required',
      description: 'The Cloud SQL instance does not require SSL connections.',
      serviceName: 'CloudSQL',
      recommendations: ['Require SSL connections to encrypt data in transit.'],
      mitigations: [
        `
\`\`\`
gcloud sql instances patch INSTANCE --require-ssl
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/sql/docs/mysql/authorize-ssl'],
    },
    gcp_storage_public_access: {
      title: 'Cloud Storage Bucket Has Public Access',
      description:
        'The GCP Cloud Storage bucket is publicly accessible, allowing anyone on the internet to read its contents. Public buckets are a leading cause of cloud data breaches.',
      serviceName: 'CloudStorage',
      recommendations: ['Remove public access unless the bucket is intended to serve public content.'],
      mitigations: ['Remove allUsers and allAuthenticatedUsers IAM bindings.'],
      references: ['https://cloud.google.com/storage/docs/public-access-prevention'],
    },
    gcp_storage_no_cmek: {
      title: 'Cloud Storage Not Using CMEK',
      description: 'The bucket is not encrypted with a customer-managed encryption key.',
      serviceName: 'CloudStorage',
      recommendations: ['Use CMEK for buckets containing sensitive data.'],
      mitigations: ['Set the default KMS key for the bucket.'],
      references: ['https://cloud.google.com/storage/docs/encryption/customer-managed-keys'],
    },
    gcp_storage_no_ubla: {
      title: 'Cloud Storage Uniform Bucket-Level Access Disabled',
      description: 'Uniform bucket-level access is not enabled, allowing both ACLs and IAM policies.',
      serviceName: 'CloudStorage',
      recommendations: ['Enable uniform bucket-level access for simplified and consistent access management.'],
      mitigations: [
        `
\`\`\`
gsutil uniformbucketlevelaccess set on gs://BUCKET_NAME
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/storage/docs/uniform-bucket-level-access'],
    },
    // --- Azure Security ---
    azure_defender_free_tier: {
      title: 'Azure Defender Using Free Tier',
      description: 'Microsoft Defender for Cloud is using the free tier with limited security capabilities.',
      serviceName: 'AzureDefender',
      recommendations: ['Upgrade to the Standard tier for enhanced threat protection.'],
      mitigations: ['Enable the Standard pricing tier for Defender plans.'],
      references: ['https://learn.microsoft.com/en-us/azure/defender-for-cloud/defender-for-cloud-introduction'],
    },
    azure_defender_unhealthy_assessment: {
      title: 'Azure Defender Unhealthy Security Assessment',
      description: 'Microsoft Defender has identified an unhealthy security assessment.',
      serviceName: 'AzureDefender',
      recommendations: ['Remediate the unhealthy assessment to improve security posture.'],
      mitigations: ['Review and apply the recommended remediation steps.'],
      references: ['https://learn.microsoft.com/en-us/azure/defender-for-cloud/managing-and-responding-alerts'],
    },
    azure_defender_auto_provision_disabled: {
      title: 'Azure Defender Auto-Provisioning Disabled',
      description: 'Automatic provisioning of security agents is disabled.',
      serviceName: 'AzureDefender',
      recommendations: ['Enable auto-provisioning to automatically install security agents on new VMs.'],
      mitigations: ['Enable auto-provisioning in Defender settings.'],
      references: ['https://learn.microsoft.com/en-us/azure/defender-for-cloud/monitoring-components'],
    },
    azure_defender_no_security_contacts: {
      title: 'Azure Defender No Security Contacts Configured',
      description: 'No security contact email addresses are configured for alert notifications.',
      serviceName: 'AzureDefender',
      recommendations: ['Configure security contacts to receive alert notifications.'],
      mitigations: ['Add security contact email addresses in Defender settings.'],
      references: ['https://learn.microsoft.com/en-us/azure/defender-for-cloud/configure-email-notifications'],
    },
    azure_storage_https_only_disabled: {
      title: 'Azure Storage HTTPS Only Disabled',
      description: 'The storage account allows HTTP connections, transmitting data unencrypted.',
      serviceName: 'AzureStorage',
      recommendations: ['Enable HTTPS-only transfer to encrypt data in transit.'],
      mitigations: [
        `
**Enable HTTPS-only:**
\`\`\`
az storage account update --https-only true --name ACCOUNT
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-require-secure-transfer'],
    },
    azure_storage_minimum_tls_version: {
      title: 'Azure Storage Minimum TLS Version Not Set',
      description: 'The storage account allows connections with older TLS versions.',
      serviceName: 'AzureStorage',
      recommendations: ['Set minimum TLS version to 1.2.'],
      mitigations: [
        `
\`\`\`
az storage account update --min-tls-version TLS1_2 --name ACCOUNT
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/transport-layer-security-configure-minimum-version'],
    },
    azure_storage_blob_public_access_enabled: {
      title: 'Azure Storage Blob Public Access Enabled',
      description: 'Public access is enabled for blob containers in the storage account.',
      serviceName: 'AzureStorage',
      recommendations: ['Disable blob public access unless specifically needed.'],
      mitigations: [
        `
\`\`\`
az storage account update --allow-blob-public-access false --name ACCOUNT
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/storage/blobs/anonymous-read-access-configure'],
    },
    azure_storage_shared_key_access_enabled: {
      title: 'Azure Storage Shared Key Access Enabled',
      description: 'Shared key authorization is enabled, which is less secure than Azure AD authentication.',
      serviceName: 'AzureStorage',
      recommendations: ['Disable shared key access and use Azure AD authentication.'],
      mitigations: ['Disable shared key access in the storage account configuration.'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/shared-key-authorization-prevent'],
    },
    azure_storage_infrastructure_encryption_disabled: {
      title: 'Azure Storage Infrastructure Encryption Disabled',
      description: 'Infrastructure encryption (double encryption) is not enabled.',
      serviceName: 'AzureStorage',
      recommendations: ['Enable infrastructure encryption for additional data protection.'],
      mitigations: ['Infrastructure encryption can only be enabled at creation time.'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/infrastructure-encryption-enable'],
    },
    azure_storage_firewall_not_configured: {
      title: 'Azure Storage Firewall Not Configured',
      description: 'The storage account does not have network rules configured.',
      serviceName: 'AzureStorage',
      recommendations: ['Configure firewall rules to restrict access to trusted networks.'],
      mitigations: [
        `
**Configure network rules:**
\`\`\`
az storage account update --default-action Deny --name ACCOUNT
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-network-security'],
    },
    azure_sql_public_network_access_enabled: {
      title: 'Azure SQL Public Network Access Enabled',
      description: 'The SQL server allows connections from the public internet.',
      serviceName: 'AzureSQL',
      recommendations: ['Disable public network access and use private endpoints.'],
      mitigations: ['Disable public access in the SQL server firewall settings.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-sql/database/connectivity-settings'],
    },
    azure_sql_entra_id_admin_not_configured: {
      title: 'Azure SQL Entra ID Admin Not Configured',
      description: 'No Azure Entra ID (AAD) administrator is configured for the SQL server.',
      serviceName: 'AzureSQL',
      recommendations: ['Configure an Entra ID admin for Azure AD authentication support.'],
      mitigations: ['Set an Entra ID admin in the SQL server configuration.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-sql/database/authentication-aad-configure'],
    },
    azure_sql_transparent_data_encryption_disabled: {
      title: 'Azure SQL Transparent Data Encryption Disabled',
      description:
        'Transparent Data Encryption (TDE) is not enabled for this Azure SQL database. TDE encrypts data at rest to protect against offline access to the database files.',
      serviceName: 'AzureSQL',
      recommendations: ['Enable TDE to encrypt data at rest.'],
      mitigations: ['Enable TDE in the database security settings.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-sql/database/transparent-data-encryption-tde-overview'],
    },
    azure_sql_advanced_data_security_disabled: {
      title: 'Azure SQL Advanced Data Security Disabled',
      description:
        'Advanced data security features are not enabled for this Azure SQL resource. Advanced data security provides vulnerability assessment and advanced threat protection capabilities.',
      serviceName: 'AzureSQL',
      recommendations: ['Enable advanced data security for threat detection and vulnerability assessment.'],
      mitigations: ['Enable advanced data security in the SQL server settings.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-sql/database/azure-defender-for-sql'],
    },
    azure_aks_rbac_disabled: {
      title: 'AKS RBAC Disabled',
      description: 'Kubernetes RBAC is not enabled on the AKS cluster.',
      serviceName: 'AzureAKS',
      recommendations: ['Enable RBAC for fine-grained access control.'],
      mitigations: ['RBAC can only be enabled at cluster creation time. Recreate the cluster with RBAC enabled.'],
      references: ['https://learn.microsoft.com/en-us/azure/aks/manage-azure-rbac'],
    },
    azure_aks_network_policy_disabled: {
      title: 'AKS Network Policy Disabled',
      description:
        'Network policy is not enabled on this AKS cluster. Without network policies, all pods can communicate freely, violating the principle of least privilege and increasing lateral movement risk.',
      serviceName: 'AzureAKS',
      recommendations: ['Enable network policy for pod-to-pod traffic control.'],
      mitigations: ['Enable network policy (Azure or Calico) in the cluster configuration.'],
      references: ['https://learn.microsoft.com/en-us/azure/aks/use-network-policies'],
    },
    azure_aks_azure_policy_disabled: {
      title: 'AKS Azure Policy Addon Disabled',
      description: 'The Azure Policy addon is not enabled on the AKS cluster.',
      serviceName: 'AzureAKS',
      recommendations: ['Enable Azure Policy addon for governance and compliance.'],
      mitigations: [
        `
**Enable the Azure Policy addon:**
\`\`\`
az aks enable-addons --addons azure-policy
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/aks/use-azure-policy'],
    },
    azure_app_service_https_only_disabled: {
      title: 'App Service HTTPS Only Disabled',
      description:
        'The Azure App Service allows unencrypted HTTP connections. All traffic should be redirected to HTTPS to protect data in transit from interception.',
      serviceName: 'AzureAppService',
      recommendations: ['Enable HTTPS only to encrypt all traffic.'],
      mitigations: [
        `
\`\`\`
az webapp update --set httpsOnly=true --name APP --resource-group RG
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/app-service/configure-ssl-bindings'],
    },
    azure_function_https_only_disabled: {
      title: 'Azure Function HTTPS Only Disabled',
      description: 'The Azure Function app allows unencrypted HTTP connections. Enforce HTTPS-only to ensure all traffic is encrypted in transit.',
      serviceName: 'AzureFunctions',
      recommendations: ['Enable HTTPS only for the function app.'],
      mitigations: [
        `
\`\`\`
az functionapp update --set httpsOnly=true --name APP --resource-group RG
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/azure-functions/security-concepts'],
    },
    azure_function_authentication_disabled: {
      title: 'Azure Function Authentication Disabled',
      description: 'Authentication is not enabled for the function app.',
      serviceName: 'AzureFunctions',
      recommendations: ['Enable authentication to protect the function app.'],
      mitigations: ['Enable authentication in the function app settings.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-functions/security-concepts'],
    },
    azure_keyvault_soft_delete_disabled: {
      title: 'Key Vault Soft Delete Disabled',
      description:
        'Soft delete is not enabled for this Azure Key Vault. Without soft delete, accidentally deleted vaults, keys, secrets, and certificates cannot be recovered.',
      serviceName: 'AzureKeyVault',
      recommendations: ['Enable soft delete to protect against accidental deletion.'],
      mitigations: ['Enable soft delete in the Key Vault properties.'],
      references: ['https://learn.microsoft.com/en-us/azure/key-vault/general/soft-delete-overview'],
    },
    azure_keyvault_purge_protection_disabled: {
      title: 'Key Vault Purge Protection Disabled',
      description: 'Purge protection is not enabled, allowing permanent deletion of keys during the retention period.',
      serviceName: 'AzureKeyVault',
      recommendations: ['Enable purge protection for Key Vaults storing critical keys.'],
      mitigations: ['Enable purge protection (cannot be disabled once enabled).'],
      references: ['https://learn.microsoft.com/en-us/azure/key-vault/general/soft-delete-overview'],
    },
    azure_sentinel_alert_rule_disabled: {
      title: 'Sentinel Alert Rule Disabled',
      description:
        'A Microsoft Sentinel analytics rule is disabled and will not detect threats. Disabled rules create gaps in security monitoring coverage.',
      serviceName: 'AzureSentinel',
      recommendations: ['Enable the rule or remove if no longer needed.'],
      mitigations: ['Enable the alert rule in Sentinel.'],
      references: ['https://learn.microsoft.com/en-us/azure/sentinel/detect-threats-built-in'],
    },
    azure_sentinel_no_automation_rules: {
      title: 'Sentinel Has No Automation Rules',
      description: 'No automation rules are configured for incident response.',
      serviceName: 'AzureSentinel',
      recommendations: ['Create automation rules to speed up incident response.'],
      mitigations: ['Configure automation rules for common incident types.'],
      references: ['https://learn.microsoft.com/en-us/azure/sentinel/automate-incident-handling-with-automation-rules'],
    },
    azure_sentinel_no_data_connectors: {
      title: 'Sentinel Has No Data Connectors',
      description: 'No data connectors are configured to ingest security data.',
      serviceName: 'AzureSentinel',
      recommendations: ['Connect data sources to enable threat detection.'],
      mitigations: ['Configure data connectors for your security data sources.'],
      references: ['https://learn.microsoft.com/en-us/azure/sentinel/connect-data-sources'],
    },
    azure_container_registry_admin_user_enabled: {
      title: 'Container Registry Admin User Enabled',
      description: 'The admin user is enabled, which provides unrestricted access.',
      serviceName: 'AzureContainerRegistry',
      recommendations: ['Disable the admin user and use Azure AD authentication.'],
      mitigations: [
        `
\`\`\`
az acr update --admin-enabled false --name REGISTRY
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/container-registry/container-registry-authentication'],
    },
    azure_container_registry_public_network_access_enabled: {
      title: 'Container Registry Public Network Access Enabled',
      description: 'The registry is accessible from the public internet.',
      serviceName: 'AzureContainerRegistry',
      recommendations: ['Disable public access and use private endpoints.'],
      mitigations: ['Configure network rules to restrict access.'],
      references: ['https://learn.microsoft.com/en-us/azure/container-registry/container-registry-private-link'],
    },
    azure_entra_id_overly_permissive_role: {
      title: 'Entra ID Overly Permissive Role Assignment',
      description: 'A user or service principal has an overly permissive directory role.',
      serviceName: 'AzureEntraID',
      recommendations: ['Review role assignments and apply the principle of least privilege.'],
      mitigations: ['Remove unnecessary role assignments and use more targeted roles.'],
      references: ['https://learn.microsoft.com/en-us/entra/identity/role-based-access-control/best-practices'],
    },
    azure_entra_id_service_principal_credentials_expired: {
      title: 'Entra ID Service Principal Credentials Expired',
      description:
        'An Azure Entra ID service principal has expired credentials. Expired credentials can cause authentication failures for applications and automated processes that depend on this identity.',
      serviceName: 'AzureEntraID',
      recommendations: ['Rotate the expired credentials immediately.'],
      mitigations: ['Generate new credentials for the service principal.'],
      references: ['https://learn.microsoft.com/en-us/entra/identity/enterprise-apps/manage-certificates-for-federated-single-sign-on'],
    },
    azure_entra_id_service_principal_credentials_expiring_soon: {
      title: 'Entra ID Service Principal Credentials Expiring Soon',
      description: 'A service principal has credentials that will expire soon.',
      serviceName: 'AzureEntraID',
      recommendations: ['Rotate credentials before they expire to prevent service disruptions.'],
      mitigations: ['Generate new credentials and update dependent applications.'],
      references: ['https://learn.microsoft.com/en-us/entra/identity/enterprise-apps/manage-certificates-for-federated-single-sign-on'],
    },
    azure_frontdoor_enable_waf: {
      title: 'Front Door WAF Should Be Enabled',
      description:
        'Web Application Firewall (WAF) is not enabled on the Azure Front Door profile. WAF provides protection against common web exploits such as SQL injection and cross-site scripting.',
      serviceName: 'AzureFrontDoor',
      recommendations: ['Enable WAF to protect against web application attacks.'],
      mitigations: ['Create and associate a WAF policy with the Front Door endpoint.'],
      references: ['https://learn.microsoft.com/en-us/azure/web-application-firewall/afds/afds-overview'],
    },
    azure_appgateway_waf_disabled: {
      title: 'Application Gateway WAF Disabled',
      description:
        'Web Application Firewall (WAF) is not enabled on this Azure Application Gateway. WAF protects web applications from common attacks and vulnerabilities.',
      serviceName: 'AzureAppGateway',
      recommendations: ['Enable WAF for web application protection.'],
      mitigations: ['Upgrade to WAF v2 SKU and enable WAF.'],
      references: ['https://learn.microsoft.com/en-us/azure/web-application-firewall/ag/ag-overview'],
    },
    azure_nsg_overly_permissive_inbound: {
      title: 'NSG Has Overly Permissive Inbound Rule',
      description: 'A Network Security Group has an inbound rule that allows traffic from any source.',
      serviceName: 'AzureNSG',
      recommendations: ['Restrict inbound rules to specific source IP ranges.'],
      mitigations: ['Update the NSG rule to restrict source addresses.'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/network-security-groups-overview'],
    },
    azure_vnet_ddos_protection_disabled: {
      title: 'VNet DDoS Protection Disabled',
      description: 'DDoS protection is not enabled for the virtual network.',
      serviceName: 'AzureVNet',
      recommendations: ['Enable DDoS protection for production virtual networks.'],
      mitigations: ['Associate a DDoS protection plan with the virtual network.'],
      references: ['https://learn.microsoft.com/en-us/azure/ddos-protection/ddos-protection-overview'],
    },
    azure_vnet_subnet_without_nsg: {
      title: 'VNet Subnet Without NSG',
      description: 'A subnet does not have a Network Security Group associated.',
      serviceName: 'AzureVNet',
      recommendations: ['Associate an NSG with every subnet for traffic filtering.'],
      mitigations: ['Create and associate an NSG with the subnet.'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/network-security-groups-overview'],
    },
    // --- Native Cloud Provider Recommendations ---
    gcp_native_iam_policy: {
      title: 'GCP IAM Policy Recommendation',
      description: 'Google Cloud recommends changes to IAM policies to follow the principle of least privilege.',
      serviceName: 'GCPIAM',
      recommendations: ['Review and apply the recommended IAM policy changes to reduce over-permissioned roles.'],
      mitigations: ['Apply the recommended IAM policy adjustments in the GCP Console or via gcloud CLI.'],
      references: ['https://cloud.google.com/recommender/docs/recommenders'],
    },
    gcp_native_cloudsql_instance_security: {
      title: 'GCP Cloud SQL Security Recommendation',
      description: 'Google Cloud has identified a security recommendation for your Cloud SQL instance.',
      serviceName: 'CloudSQL',
      recommendations: ['Review and apply the security recommendation to improve your Cloud SQL instance security posture.'],
      mitigations: ['Apply the recommended security settings in the Cloud SQL console.'],
      references: ['https://cloud.google.com/sql/docs/mysql/best-practices'],
    },
    azure_native_advisor_security: {
      title: 'Azure Advisor Security Recommendation',
      description: 'Azure Advisor has identified a security improvement opportunity for your resources.',
      serviceName: 'AzureAdvisor',
      recommendations: ['Review and implement the Azure Advisor security recommendation.'],
      mitigations: ['Follow the remediation steps provided in the Azure Advisor recommendation details.'],
      references: ['https://learn.microsoft.com/en-us/azure/advisor/advisor-security-recommendations'],
    },
    aws_elasticache_encryption_at_rest: {
      title: 'ElastiCache Encryption at Rest Should Be Enabled',
      description:
        'Amazon ElastiCache clusters should have encryption at rest enabled to protect sensitive data stored in cache. Encryption at rest uses AWS KMS keys to encrypt the data on disk, backups, and replicas, ensuring compliance with data protection regulations.',
      serviceName: 'AmazonElastiCache',
      recommendations: [
        'Enable encryption at rest for ElastiCache clusters. Note that encryption at rest can only be enabled during cluster creation \u2014 existing clusters without encryption must be recreated with encryption enabled.',
      ],
      mitigations: [
        `Create a new ElastiCache cluster with encryption at rest enabled:
\`\`\`
aws elasticache create-replication-group \\
  --replication-group-id {{cluster_id}}-encrypted \\
  --replication-group-description "Encrypted replica" \\
  --at-rest-encryption-enabled \\
  --cache-node-type {{node_type}} \\
  --engine redis
\`\`\`

Migrate data from the unencrypted cluster to the new encrypted cluster, then update your application configuration to point to the new cluster.`,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/at-rest-encryption.html'],
    },
    aws_elasticache_encryption_in_transit: {
      title: 'ElastiCache Encryption in Transit Should Be Enabled',
      description:
        'Amazon ElastiCache clusters should have encryption in transit (TLS) enabled to protect data as it moves between clients and the cache cluster. Without encryption in transit, data including credentials and sensitive application data can be intercepted.',
      serviceName: 'AmazonElastiCache',
      recommendations: [
        'Enable encryption in transit for ElastiCache clusters to ensure all communication between clients and the cache is encrypted using TLS. Update client applications to connect using TLS-enabled endpoints.',
      ],
      mitigations: [
        `Create a new ElastiCache cluster with encryption in transit enabled:
\`\`\`
aws elasticache create-replication-group \\
  --replication-group-id {{cluster_id}}-tls \\
  --replication-group-description "TLS-enabled replica" \\
  --transit-encryption-enabled \\
  --cache-node-type {{node_type}} \\
  --engine redis
\`\`\`

Update application connection strings to use the TLS endpoint and port 6380.`,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/in-transit-encryption.html'],
    },
    aws_guardduty_not_enabled: {
      title: 'AWS GuardDuty Should Be Enabled',
      description:
        'Amazon GuardDuty is a threat detection service that continuously monitors for malicious activity and unauthorized behavior. It analyzes CloudTrail events, VPC Flow Logs, and DNS logs to identify threats such as compromised instances, reconnaissance, and account compromise.',
      serviceName: 'AmazonGuardDuty',
      recommendations: [
        'Enable Amazon GuardDuty in all AWS regions to ensure comprehensive threat detection coverage. Configure GuardDuty to send findings to a central security account for aggregated monitoring.',
      ],
      mitigations: [
        `Enable GuardDuty in the current region:
\`\`\`
aws guardduty create-detector \\
  --enable \\
  --finding-publishing-frequency FIFTEEN_MINUTES
\`\`\`

To enable in all regions:
\`\`\`
for region in $(aws ec2 describe-regions --query 'Regions[].RegionName' --output text); do
  aws guardduty create-detector --enable --region $region
done
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'APRA', 'MAS'],
      references: ['https://docs.aws.amazon.com/guardduty/latest/ug/guardduty_settingup.html'],
    },
    azure_aks_enable_rbac: {
      title: 'Azure AKS RBAC Should Be Enabled',
      description: 'RBAC is not enabled on this AKS cluster. Without RBAC, all authenticated users have full cluster admin access.',
      serviceName: 'AKS',
      recommendations: ['RBAC is not enabled on this AKS cluster. Without RBAC, all authenticated users have full cluster admin access.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/aks/manage-azure-rbac'],
    },
    azure_app_service_client_cert_disabled: {
      title: 'Azure App Service Client Certificate Should Be Required',
      description: 'Client certificate authentication is not enabled, meaning any client can access the service without presenting a certificate.',
      serviceName: 'AppService',
      recommendations: [
        'Client certificate authentication is not enabled, meaning any client can access the service without presenting a certificate.',
      ],
      mitigations: [
        'Enable App Service Client Certificate Should Be Required through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/app-service/app-service-web-configure-tls-mutual-auth'],
    },
    azure_bot_service_managed_identity_disabled: {
      title: 'Azure Bot Service Managed Identity Should Be Enabled',
      description: 'Managed identity is not configured for this Bot Service, requiring credentials to be stored in configuration.',
      serviceName: 'BotService',
      recommendations: ['Managed identity is not configured for this Bot Service, requiring credentials to be stored in configuration.'],
      mitigations: [
        'Enable Bot Service Managed Identity through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/bot-service/bot-builder-authentication-managed-identity'],
    },
    azure_bot_service_public_network_access_enabled: {
      title: 'Azure Bot Service Public Network Access Should Be Restricted',
      description: 'Public network access is enabled, exposing the Bot Service to the internet.',
      serviceName: 'BotService',
      recommendations: ['Public network access is enabled, exposing the Bot Service to the internet.'],
      mitigations: [
        'Disable or restrict Bot Service Public Network Access through the Azure Portal or using Azure CLI. Refer to the documentation for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/bot-service/dl-network-isolation-concept'],
    },
    azure_container_app_insecure_ingress: {
      title: 'Azure Container App Ingress Should Require HTTPS',
      description: 'The Container App allows HTTP traffic. All traffic should be encrypted using HTTPS.',
      serviceName: 'ContainerApps',
      recommendations: ['The Container App allows HTTP traffic. All traffic should be encrypted using HTTPS.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/container-apps/ingress-overview'],
    },
    azure_container_app_no_managed_identity: {
      title: 'Azure Container App Should Use Managed Identity',
      description: 'Managed identity is not configured, requiring credentials to be stored in configuration or environment variables.',
      serviceName: 'ContainerApps',
      recommendations: ['Managed identity is not configured, requiring credentials to be stored in configuration or environment variables.'],
      mitigations: [
        'Configure Container App Should Use Managed Identity through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/container-apps/managed-identity'],
    },
    azure_container_app_public_ingress_no_auth: {
      title: 'Azure Container App Public Ingress Has No Authentication',
      description: 'The Container App is publicly accessible without authentication configured.',
      serviceName: 'ContainerApps',
      recommendations: ['The Container App is publicly accessible without authentication configured.'],
      mitigations: [
        'Configure Container App Public Ingress Authentication through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/container-apps/authentication'],
    },
    azure_container_app_secrets_not_keyvault: {
      title: 'Azure Container App Secrets Should Reference Key Vault',
      description: 'Container App secrets are stored directly instead of referencing Azure Key Vault, increasing the risk of secret exposure.',
      serviceName: 'ContainerApps',
      recommendations: ['Container App secrets are stored directly instead of referencing Azure Key Vault, increasing the risk of secret exposure.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/container-apps/manage-secrets'],
    },
    azure_container_registry_arm_token_auth_enabled: {
      title: 'Azure Container Registry ARM Token Auth Should Be Disabled',
      description: 'ARM audience token authentication is enabled, which is less secure than AAD token authentication.',
      serviceName: 'ContainerRegistry',
      recommendations: ['ARM audience token authentication is enabled, which is less secure than AAD token authentication.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/container-registry/container-registry-authentication'],
    },
    azure_container_registry_cmk_encryption_disabled: {
      title: 'Azure Container Registry Should Use CMK Encryption',
      description: 'Customer-managed key encryption is not enabled on this container registry.',
      serviceName: 'ContainerRegistry',
      recommendations: ['Customer-managed key encryption is not enabled on this container registry.'],
      mitigations: [
        'Enable Container Registry Should Use CMK Encryption through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/container-registry/container-registry-customer-managed-keys'],
    },
    azure_container_registry_no_ip_rules: {
      title: 'Azure Container Registry Should Have IP Rules',
      description: 'No IP access rules are configured, allowing connections from any network.',
      serviceName: 'ContainerRegistry',
      recommendations: ['No IP access rules are configured, allowing connections from any network.'],
      mitigations: [
        'Configure Container Registry IP Rules through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/container-registry/container-registry-access-selected-networks'],
    },
    azure_container_registry_no_managed_identity: {
      title: 'Azure Container Registry Should Use Managed Identity',
      description: 'Managed identity is not configured for this container registry.',
      serviceName: 'ContainerRegistry',
      recommendations: ['Managed identity is not configured for this container registry.'],
      mitigations: [
        'Configure Container Registry Should Use Managed Identity through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/container-registry/container-registry-authentication-managed-identity'],
    },
    azure_container_registry_no_private_endpoints: {
      title: 'Azure Container Registry Should Use Private Endpoints',
      description: 'No private endpoints are configured, meaning traffic goes over the public internet.',
      serviceName: 'ContainerRegistry',
      recommendations: ['No private endpoints are configured, meaning traffic goes over the public internet.'],
      mitigations: [
        'Configure Container Registry Should Use Private Endpoints through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/container-registry/container-registry-private-link'],
    },
    azure_container_registry_trusted_ms_disabled: {
      title: 'Azure Container Registry Trusted Microsoft Services Should Be Allowed',
      description: 'Trusted Microsoft services are not allowed to bypass network restrictions.',
      serviceName: 'ContainerRegistry',
      recommendations: ['Trusted Microsoft services are not allowed to bypass network restrictions.'],
      mitigations: [
        'Enable Container Registry Trusted Microsoft Services Should Be Allowed through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/container-registry/allow-access-trusted-services'],
    },
    azure_ddos_protection_no_plan: {
      title: 'Azure DDoS Protection Plan Should Be Created',
      description: 'No DDoS Protection Plan exists in this subscription. DDoS Protection Standard provides enhanced mitigation capabilities.',
      serviceName: 'DDoSProtection',
      recommendations: ['No DDoS Protection Plan exists in this subscription. DDoS Protection Standard provides enhanced mitigation capabilities.'],
      mitigations: [
        'Configure DDoS Protection Plan Should Be Created through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/ddos-protection/ddos-protection-overview'],
    },
    azure_ddos_protection_plan_no_vnets: {
      title: 'Azure DDoS Protection Plan Has No Associated VNets',
      description: 'The DDoS Protection Plan exists but has no virtual networks associated with it.',
      serviceName: 'DDoSProtection',
      recommendations: ['The DDoS Protection Plan exists but has no virtual networks associated with it.'],
      mitigations: [
        'Configure DDoS Protection Plan Associated VNets through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/ddos-protection/manage-ddos-protection'],
    },
    azure_ddos_protection_public_ip_disabled: {
      title: 'Azure DDoS Protection Public IP Not Protected',
      description: 'A public IP address is not protected by DDoS Protection.',
      serviceName: 'DDoSProtection',
      recommendations: ['A public IP address is not protected by DDoS Protection.'],
      mitigations: [
        'Enable DDoS Protection Public IP Not Protected through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/ddos-protection/manage-ddos-protection'],
    },
    azure_ddos_protection_public_ip_not_protected: {
      title: 'Azure Public IP Without DDoS Protection',
      description: 'A public IP resource is not covered by any DDoS Protection Plan.',
      serviceName: 'DDoSProtection',
      recommendations: ['A public IP resource is not covered by any DDoS Protection Plan.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/ddos-protection/manage-ddos-protection'],
    },
    azure_ddos_protection_vnet_not_protected: {
      title: 'Azure VNet Not Protected by DDoS Protection',
      description: 'A virtual network is not associated with any DDoS Protection Plan.',
      serviceName: 'DDoSProtection',
      recommendations: ['A virtual network is not associated with any DDoS Protection Plan.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/ddos-protection/manage-ddos-protection'],
    },
    azure_devops_project_public_visibility: {
      title: 'Azure DevOps Project Should Not Be Public',
      description: 'The project is publicly visible, potentially exposing source code and work items.',
      serviceName: 'AzureDevOps',
      recommendations: ['The project is publicly visible, potentially exposing source code and work items.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/devops/organizations/projects/make-project-public'],
    },
    azure_disk_public_network_access_enabled: {
      title: 'Azure Managed Disk Public Access Should Be Disabled',
      description: 'Public network access is enabled for disk export/import operations.',
      serviceName: 'ManagedDisks',
      recommendations: ['Public network access is enabled for disk export/import operations.'],
      mitigations: [
        'Disable or restrict Managed Disk Public Access through the Azure Portal or using Azure CLI. Refer to the documentation for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/disks-enable-private-links-for-import-export-portal'],
    },
    azure_disk_unattached_cmk_missing: {
      title: 'Azure Unattached Disk Should Use CMK Encryption',
      description: 'An unattached disk is not encrypted with customer-managed keys.',
      serviceName: 'ManagedDisks',
      recommendations: ['An unattached disk is not encrypted with customer-managed keys.'],
      mitigations: [
        'Configure Unattached Disk Should Use CMK Encryption through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption'],
    },
    azure_disk_unattached_unencrypted: {
      title: 'Azure Unattached Disk Is Not Encrypted',
      description: 'An unattached disk is not encrypted, potentially exposing data at rest.',
      serviceName: 'ManagedDisks',
      recommendations: ['An unattached disk is not encrypted, potentially exposing data at rest.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption-overview'],
    },
    azure_dns_add_caa_record: {
      title: 'Azure DNS Zone Should Have CAA Records',
      description: 'No CAA records are configured, allowing any CA to issue certificates for the domain.',
      serviceName: 'DNS',
      recommendations: ['No CAA records are configured, allowing any CA to issue certificates for the domain.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/dns/dns-faq'],
    },
    azure_entra_id_guest_with_privileged_role: {
      title: 'Azure Entra ID Guest Users Should Not Have Privileged Roles',
      description: 'Guest users have been assigned privileged roles, which could lead to unauthorized access.',
      serviceName: 'EntraID',
      recommendations: ['Guest users have been assigned privileged roles, which could lead to unauthorized access.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/entra/identity/role-based-access-control/security-planning'],
    },
    azure_entra_id_service_principal_certificates_expired: {
      title: 'Azure Service Principal Certificate Has Expired',
      description: "A service principal's certificate has expired, potentially causing authentication failures.",
      serviceName: 'EntraID',
      recommendations: ["A service principal's certificate has expired, potentially causing authentication failures."],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/entra/identity-platform/certificate-credentials'],
    },
    azure_entra_id_service_principal_certificates_expiring_soon: {
      title: 'Azure Service Principal Certificate Expiring Soon',
      description: "A service principal's certificate will expire soon and should be renewed.",
      serviceName: 'EntraID',
      recommendations: ["A service principal's certificate will expire soon and should be renewed."],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/entra/identity-platform/certificate-credentials'],
    },
    azure_eventgrid_domain_local_auth_enabled: {
      title: 'Azure Event Grid Domain Local Auth Should Be Disabled',
      description: 'Local authentication is enabled, which is less secure than Azure AD authentication.',
      serviceName: 'EventGrid',
      recommendations: ['Local authentication is enabled, which is less secure than Azure AD authentication.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/event-grid/authenticate-with-access-keys-shared-access-signatures'],
    },
    azure_eventgrid_domain_public_access_enabled: {
      title: 'Azure Event Grid Domain Public Access Should Be Restricted',
      description: 'Public network access is enabled on the Event Grid domain.',
      serviceName: 'EventGrid',
      recommendations: ['Public network access is enabled on the Event Grid domain.'],
      mitigations: [
        'Disable or restrict Event Grid Domain Public Access through the Azure Portal or using Azure CLI. Refer to the documentation for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/event-grid/configure-firewall'],
    },
    azure_eventgrid_topic_no_managed_identity: {
      title: 'Azure Event Grid Topic Should Use Managed Identity',
      description: 'Managed identity is not configured for this Event Grid topic.',
      serviceName: 'EventGrid',
      recommendations: ['Managed identity is not configured for this Event Grid topic.'],
      mitigations: [
        'Configure Event Grid Topic Should Use Managed Identity through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/event-grid/managed-service-identity'],
    },
    azure_eventgrid_topic_public_access_no_ip_filter: {
      title: 'Azure Event Grid Topic Public Access Without IP Filter',
      description: 'Public access is enabled without IP filtering, allowing connections from any network.',
      serviceName: 'EventGrid',
      recommendations: ['Public access is enabled without IP filtering, allowing connections from any network.'],
      mitigations: [
        'Configure Event Grid Topic Public Access Without IP Filter through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/event-grid/configure-firewall'],
    },
    azure_files_enable_smb_encryption: {
      title: 'Azure Files SMB Encryption Should Be Enabled',
      description: 'SMB encryption is not enabled for Azure Files, leaving data in transit unencrypted.',
      serviceName: 'AzureFiles',
      recommendations: ['SMB encryption is not enabled for Azure Files, leaving data in transit unencrypted.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/files/storage-files-smb-multichannel-performance'],
    },
    azure_firewall_enable_threat_intel: {
      title: 'Azure Firewall Threat Intelligence Should Be Enabled',
      description: 'Threat intelligence-based filtering is not enabled, missing automatic blocking of known malicious IPs.',
      serviceName: 'AzureFirewall',
      recommendations: ['Threat intelligence-based filtering is not enabled, missing automatic blocking of known malicious IPs.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/firewall/threat-intel'],
    },
    azure_frontdoor_enable_https_redirect: {
      title: 'Azure Front Door HTTPS Redirect Should Be Enabled',
      description: 'HTTP to HTTPS redirect is not configured, allowing unencrypted connections.',
      serviceName: 'FrontDoor',
      recommendations: ['HTTP to HTTPS redirect is not configured, allowing unencrypted connections.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/frontdoor/front-door-how-to-redirect-https'],
    },
    azure_mariadb_ssl_disabled: {
      title: 'Azure MariaDB SSL Enforcement Should Be Enabled',
      description: 'SSL enforcement is disabled, allowing unencrypted database connections.',
      serviceName: 'MariaDB',
      recommendations: ['SSL enforcement is disabled, allowing unencrypted database connections.'],
      mitigations: [
        'Enable MariaDB SSL Enforcement through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/mariadb/concepts-ssl-connection-security'],
    },
    azure_ml_workspace_hbi_not_enabled: {
      title: 'Azure ML Workspace High Business Impact Should Be Enabled',
      description: 'High business impact mode is not enabled, which provides additional data isolation.',
      serviceName: 'MachineLearning',
      recommendations: ['High business impact mode is not enabled, which provides additional data isolation.'],
      mitigations: [
        'Enable ML Workspace High Business Impact through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/machine-learning/concept-data-encryption'],
    },
    azure_ml_workspace_managed_identity_disabled: {
      title: 'Azure ML Workspace Managed Identity Should Be Enabled',
      description: 'Managed identity is not configured for the ML workspace.',
      serviceName: 'MachineLearning',
      recommendations: ['Managed identity is not configured for the ML workspace.'],
      mitigations: [
        'Enable ML Workspace Managed Identity through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/machine-learning/how-to-identity-based-service-authentication'],
    },
    azure_ml_workspace_public_network_access_enabled: {
      title: 'Azure ML Workspace Public Network Access Should Be Disabled',
      description: 'Public network access is enabled on the ML workspace.',
      serviceName: 'MachineLearning',
      recommendations: ['Public network access is enabled on the ML workspace.'],
      mitigations: [
        'Disable or restrict ML Workspace Public Network Access through the Azure Portal or using Azure CLI. Refer to the documentation for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/machine-learning/how-to-configure-private-link'],
    },
    azure_redis_enable_non_ssl_port: {
      title: 'Azure Redis Non-SSL Port Should Be Disabled',
      description: 'The non-SSL port (6379) is enabled, allowing unencrypted connections.',
      serviceName: 'RedisCache',
      recommendations: ['The non-SSL port (6379) is enabled, allowing unencrypted connections.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-cache-for-redis/cache-configure'],
    },
    azure_redis_non_ssl_port_enabled: {
      title: 'Azure Redis Cache Non-SSL Port Is Enabled',
      description: 'Non-SSL port is enabled on this Redis Cache instance, exposing data in transit.',
      serviceName: 'RedisCache',
      recommendations: ['Non-SSL port is enabled on this Redis Cache instance, exposing data in transit.'],
      mitigations: [
        'Disable or restrict Redis Cache Non-SSL Port Is Enabled through the Azure Portal or using Azure CLI. Refer to the documentation for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-cache-for-redis/cache-configure'],
    },
    azure_redis_old_tls_version: {
      title: 'Azure Redis Cache TLS Version Should Be Updated',
      description: 'The Redis Cache uses an older TLS version with known vulnerabilities.',
      serviceName: 'RedisCache',
      recommendations: ['The Redis Cache uses an older TLS version with known vulnerabilities.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-cache-for-redis/cache-remove-tls-10-11'],
    },
    azure_redis_public_network_access: {
      title: 'Azure Redis Cache Public Network Access Should Be Restricted',
      description: 'Public network access is enabled on this Redis Cache.',
      serviceName: 'RedisCache',
      recommendations: ['Public network access is enabled on this Redis Cache.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-cache-for-redis/cache-private-link'],
    },
    azure_sentinel_no_threat_intel: {
      title: 'Azure Sentinel Threat Intelligence Not Configured',
      description: 'Threat intelligence indicators are not configured in Sentinel.',
      serviceName: 'Sentinel',
      recommendations: ['Threat intelligence indicators are not configured in Sentinel.'],
      mitigations: [
        'Configure Sentinel Threat Intelligence Not Configured through the Azure Portal or using Azure CLI. Refer to the documentation link below for detailed configuration steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/sentinel/understand-threat-intelligence'],
    },
    azure_storage_anonymous_access_enabled: {
      title: 'Azure Storage Account Anonymous Access Should Be Disabled',
      description:
        'Anonymous (public) access is enabled on this storage account, allowing unauthenticated users to read data from containers. This can lead to data exposure and is a common source of cloud data breaches.',
      serviceName: 'StorageAccounts',
      recommendations: [
        'Disable anonymous access on the storage account and all containers. Use SAS tokens or Azure AD authentication for authorized access.',
      ],
      mitigations: [
        `Disable anonymous access:
\`\`\`
az storage account update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --allow-blob-public-access false
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/blobs/anonymous-read-access-prevent'],
    },
    azure_storage_cmk_disabled: {
      title: 'Azure Storage Should Use Customer-Managed Keys',
      description:
        'This storage account uses Microsoft-managed keys for encryption. Customer-managed keys (CMK) provide additional control over encryption keys, including rotation, revocation, and audit capabilities.',
      serviceName: 'StorageAccounts',
      recommendations: ['Configure customer-managed keys stored in Azure Key Vault for storage account encryption.'],
      mitigations: [
        `Configure CMK encryption:
\`\`\`
az storage account update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --encryption-key-source Microsoft.Keyvault \\
  --encryption-key-vault {{keyvault_uri}} \\
  --encryption-key-name {{key_name}}
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/customer-managed-keys-overview'],
    },
    azure_storage_minimum_tls_version_not_set_to_1_2: {
      title: 'Azure Storage Minimum TLS Version Should Be 1.2',
      description:
        'This storage account allows connections using TLS versions older than 1.2. Older TLS versions (1.0, 1.1) have known vulnerabilities and should be disabled to protect data in transit.',
      serviceName: 'StorageAccounts',
      recommendations: ['Set the minimum TLS version to 1.2 for the storage account.'],
      mitigations: [
        `Update minimum TLS version:
\`\`\`
az storage account update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --min-tls-version TLS1_2
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/transport-layer-security-configure-minimum-version'],
    },
    azure_storage_public_network_access_enabled: {
      title: 'Azure Storage Public Network Access Should Be Disabled',
      description:
        'Public network access is enabled on this storage account, allowing connections from any IP address on the internet. Restricting network access to specific VNets and IP ranges reduces the attack surface.',
      serviceName: 'StorageAccounts',
      recommendations: ['Disable public network access and use private endpoints or VNet service endpoints for secure connectivity.'],
      mitigations: [
        `Disable public network access:
\`\`\`
az storage account update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --public-network-access Disabled
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-network-security'],
    },
    azure_storage_secure_transfer_disabled: {
      title: 'Azure Storage Secure Transfer Should Be Required',
      description:
        'Secure transfer (HTTPS only) is not required for this storage account. Without this setting, data can be transmitted over unencrypted HTTP connections.',
      serviceName: 'StorageAccounts',
      recommendations: ["Enable the 'Secure transfer required' setting to enforce HTTPS for all storage account operations."],
      mitigations: [
        `Enable secure transfer:
\`\`\`
az storage account update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --https-only true
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-require-secure-transfer'],
    },
    azure_storage_shared_key_authorization_enabled: {
      title: 'Azure Storage Shared Key Authorization Should Be Disabled',
      description:
        'Shared key authorization is enabled, allowing access using storage account keys. Shared keys are long-lived credentials that, if compromised, provide unrestricted access. Azure AD authentication is the recommended alternative.',
      serviceName: 'StorageAccounts',
      recommendations: ['Disable shared key access and use Azure AD authentication or SAS tokens with limited scope and expiration.'],
      mitigations: [
        `Disable shared key access:
\`\`\`
az storage account update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --allow-shared-key-access false
\`\`\`

Ensure all applications are updated to use Azure AD authentication before disabling.`,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/shared-key-authorization-prevent'],
    },
    azure_vm_boot_disk_encryption_disabled: {
      title: 'Azure VM OS Disk Encryption Should Be Enabled',
      description:
        'The OS disk of this Azure VM is not encrypted with Azure Disk Encryption (ADE) or server-side encryption with customer-managed keys. Disk encryption protects data at rest and helps meet organizational security and compliance requirements.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Enable Azure Disk Encryption on the OS disk using either platform-managed keys (default SSE) or customer-managed keys (CMK) stored in Azure Key Vault for enhanced control.',
      ],
      mitigations: [
        `Enable Azure Disk Encryption on the VM:
\`\`\`
az vm encryption enable \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --disk-encryption-keyvault {{keyvault_name}}
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption-overview'],
    },
    azure_vm_confidential_computing_disabled: {
      title: 'Azure VM Confidential Computing Should Be Enabled',
      description:
        'Azure Confidential Computing protects data in use by using hardware-based Trusted Execution Environments (TEEs). This ensures that data remains encrypted in memory and is protected from the cloud operator, making it suitable for highly sensitive workloads.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Consider deploying confidential computing VMs (DCsv2, DCsv3, or DCdsv3 series) for workloads that process highly sensitive data requiring protection from cloud operators.',
      ],
      mitigations: [
        `Deploy a confidential computing VM:
\`\`\`
az vm create \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}-confidential \\
  --size Standard_DC4s_v3 \\
  --image UbuntuLTS \\
  --security-type ConfidentialVM
\`\`\``,
      ],
      compliances: ['NIST4', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/confidential-computing/overview'],
    },
    azure_vm_data_disk_encryption_disabled: {
      title: 'Azure VM Data Disk Encryption Should Be Enabled',
      description:
        'One or more data disks attached to this Azure VM are not encrypted. Data disk encryption protects sensitive data at rest and is required for many compliance frameworks.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Enable encryption on all data disks attached to the VM using Azure Disk Encryption or server-side encryption with customer-managed keys.',
      ],
      mitigations: [
        `Enable encryption on all disks (OS and data):
\`\`\`
az vm encryption enable \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --disk-encryption-keyvault {{keyvault_name}} \\
  --volume-type DATA
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA', 'PCI-DSS'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption-overview'],
    },
    azure_vm_disk_encryption_cmk_missing: {
      title: 'Azure VM Disk Should Use Customer-Managed Keys',
      description:
        'Azure VM disks are encrypted with platform-managed keys by default, but customer-managed keys (CMK) provide additional control over the encryption keys, including the ability to rotate, disable, and audit key usage through Azure Key Vault.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Configure disk encryption with customer-managed keys stored in Azure Key Vault for enhanced encryption key management and compliance.',
      ],
      mitigations: [
        `Create a disk encryption set with a CMK and apply it:
\`\`\`
az disk-encryption-set create \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}-des \\
  --key-url {{key_url}} \\
  --source-vault {{keyvault_id}}

az vm update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --os-disk-encryption-set {{des_id}}
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2', 'HIPAA'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption'],
    },
    azure_vm_endpoint_protection_missing: {
      title: 'Azure VM Endpoint Protection Should Be Installed',
      description:
        'Endpoint protection (antimalware) is not installed on this VM. Without endpoint protection, the VM is vulnerable to malware, viruses, and other malicious software that could compromise data and workloads.',
      serviceName: 'VirtualMachines',
      recommendations: ['Install and configure Microsoft Antimalware or a supported third-party endpoint protection solution on the VM.'],
      mitigations: [
        `Install Microsoft Antimalware extension:
\`\`\`
az vm extension set \\
  --resource-group {{resource_group}} \\
  --vm-name {{resource_name}} \\
  --name IaaSAntimalware \\
  --publisher Microsoft.Azure.Security \\
  --settings '{"AntimalwareEnabled": true, "RealtimeProtectionEnabled": true}'
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/extensions/iaas-antimalware-windows'],
    },
    azure_vm_entra_id_authentication_disabled: {
      title: 'Azure VM Should Use Entra ID Authentication',
      description:
        'Microsoft Entra ID (formerly Azure AD) authentication for VMs provides centralized identity management, conditional access policies, and MFA support. Using local accounts increases the risk of credential theft and makes access auditing difficult.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Enable Microsoft Entra ID login for the VM to leverage centralized identity management, conditional access policies, and audit logging.',
      ],
      mitigations: [
        `Install the AAD login extension:
\`\`\`
az vm extension set \\
  --resource-group {{resource_group}} \\
  --vm-name {{resource_name}} \\
  --name AADSSHLoginForLinux \\
  --publisher Microsoft.Azure.ActiveDirectory
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/entra/identity/devices/howto-vm-sign-in-azure-ad-linux'],
    },
    azure_vm_jit_access_disabled: {
      title: 'Azure VM Just-In-Time Access Should Be Enabled',
      description:
        'Just-in-time (JIT) VM access reduces exposure to attacks by locking down inbound traffic to Azure VMs and providing time-limited access when needed. Without JIT, management ports (RDP/SSH) may be permanently open, increasing the attack surface.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Enable JIT VM access through Microsoft Defender for Cloud to restrict management port access and require time-limited, approved access requests.',
      ],
      mitigations: [
        `Enable JIT access via Azure Portal: Navigate to Microsoft Defender for Cloud > Workload protections > Just-in-time VM access > Enable JIT on the VM.

Or via CLI:
\`\`\`
az security jit-policy create \\
  --resource-group {{resource_group}} \\
  --location {{resource_region}} \\
  --name default \\
  --virtual-machines '[{"id": "{{resource_id}}", "ports": [{"number": 22, "protocol": "TCP", "allowedSourceAddressPrefix": "*", "maxRequestAccessDuration": "PT3H"}]}]'
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/defender-for-cloud/just-in-time-access-usage'],
    },
    azure_vm_ssh_password_authentication_enabled: {
      title: 'Azure VM SSH Password Authentication Should Be Disabled',
      description:
        'SSH password authentication is enabled on this Linux VM. Password-based SSH authentication is vulnerable to brute-force attacks. SSH key-based authentication provides stronger security and should be used instead.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Disable SSH password authentication and use SSH key-based authentication instead. Ensure SSH keys are properly configured before disabling password authentication.',
      ],
      mitigations: [
        `Disable password authentication in the VM's SSH configuration:
\`\`\`
az vm user update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --username {{admin_username}} \\
  --ssh-key-value ~/.ssh/id_rsa.pub
\`\`\`

Then SSH into the VM and disable password auth:
\`\`\`
sudo sed -i 's/PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config
sudo systemctl restart sshd
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/linux/mac-create-ssh-keys'],
    },
    azure_vm_system_assigned_identity_disabled: {
      title: 'Azure VM System-Assigned Managed Identity Should Be Enabled',
      description:
        'System-assigned managed identity provides an automatically managed identity in Microsoft Entra ID for the VM. It eliminates the need to store credentials in code and enables secure access to Azure resources through Azure RBAC.',
      serviceName: 'VirtualMachines',
      recommendations: [
        'Enable system-assigned managed identity for the VM to allow secure, credential-free access to Azure services like Key Vault, Storage, and SQL Database.',
      ],
      mitigations: [
        `Enable system-assigned managed identity:
\`\`\`
az vm identity assign \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/entra/identity/managed-identities-azure-resources/qs-configure-cli-windows-vm'],
    },
    azure_vm_trusted_launch_disabled: {
      title: 'Azure VM Trusted Launch Should Be Enabled',
      description:
        'Trusted Launch protects Azure VMs against advanced and persistent attack techniques by providing Secure Boot, vTPM, and boot integrity monitoring. Without Trusted Launch, VMs are more susceptible to rootkits and boot-level malware.',
      serviceName: 'VirtualMachines',
      recommendations: ['Enable Trusted Launch for new VM deployments to protect against boot-level attacks with Secure Boot and vTPM capabilities.'],
      mitigations: [
        `Create a new VM with Trusted Launch enabled:
\`\`\`
az vm create \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}-trusted \\
  --image UbuntuLTS \\
  --security-type TrustedLaunch \\
  --enable-secure-boot true \\
  --enable-vtpm true
\`\`\`

Note: Existing VMs cannot be upgraded to Trusted Launch. A new VM must be created.`,
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/trusted-launch'],
    },
    azure_vmss_instance_public_ip_assigned: {
      title: 'Azure VMSS Instances Should Not Have Public IPs',
      description:
        'VMSS instances have public IP addresses assigned directly, exposing them to the internet. This increases the attack surface and makes instances vulnerable to unauthorized access attempts.',
      serviceName: 'VirtualMachineScaleSets',
      recommendations: [
        'Remove public IP configurations from VMSS instances. Use Azure Load Balancer, Application Gateway, or Azure Bastion for controlled access.',
      ],
      mitigations: [
        `Remove public IP from VMSS configuration:
\`\`\`
az vmss update \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --remove virtualMachineProfile.networkProfile.networkInterfaceConfigurations[0].ipConfigurations[0].publicIPAddressConfiguration
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/virtual-machine-scale-sets-networking'],
    },
    azure_vmss_system_assigned_identity_disabled: {
      title: 'Azure VMSS System-Assigned Identity Should Be Enabled',
      description:
        'System-assigned managed identity is not enabled on this VMSS. Managed identities eliminate the need to store credentials in code and enable secure access to Azure resources.',
      serviceName: 'VirtualMachineScaleSets',
      recommendations: ['Enable system-assigned managed identity for the VMSS to allow credential-free access to Azure services.'],
      mitigations: [
        `Enable system-assigned identity:
\`\`\`
az vmss identity assign \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4', 'SOC2'],
      references: ['https://learn.microsoft.com/en-us/entra/identity/managed-identities-azure-resources/qs-configure-cli-windows-vmss'],
    },
    azure_vmss_trusted_launch_disabled: {
      title: 'Azure VMSS Trusted Launch Should Be Enabled',
      description:
        'Trusted Launch is not enabled on this VMSS. Trusted Launch protects against boot-level attacks with Secure Boot and vTPM capabilities.',
      serviceName: 'VirtualMachineScaleSets',
      recommendations: ['Enable Trusted Launch for new VMSS deployments to protect against boot-level threats.'],
      mitigations: [
        `Create a new VMSS with Trusted Launch:
\`\`\`
az vmss create \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}-trusted \\
  --security-type TrustedLaunch \\
  --enable-secure-boot true \\
  --enable-vtpm true
\`\`\``,
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/trusted-launch'],
    },
    azure_vnet_service_endpoints_not_configured: {
      title: 'Azure VNet Service Endpoints Should Be Configured',
      description: 'Service endpoints are not configured, meaning traffic to Azure services goes over the public internet.',
      serviceName: 'VirtualNetworks',
      recommendations: ['Service endpoints are not configured, meaning traffic to Azure services goes over the public internet.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/virtual-network-service-endpoints-overview'],
    },
    azure_vnet_vm_protection_disabled: {
      title: 'Azure VNet VM Protection Is Disabled',
      description:
        'VM protection is not enabled on this Azure Virtual Network. VM protection prevents accidental deletion of VMs within the VNet by requiring explicit override.',
      serviceName: 'VirtualNetworks',
      recommendations: ['VM protection is not enabled on the VNet.'],
      mitigations: [
        'Enable VNet VM Protection through the Azure Portal or using Azure CLI/Terraform. Refer to the documentation link below for detailed steps.',
      ],
      compliances: ['CIS', 'NIST4'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/virtual-networks-overview'],
    },
  },
  InfraUpgrade: {
    aws_elasticache_engine_version: {
      title: 'Use latest ElastiCache Engine Version',
      description:
        'When your Amazon ElastiCache clusters are configured with the latest version of Redis/Memcached cache engine, you can benefit from new features and enhancements, better performance, better memory management, bug fixes and security patches. For example, upgrading your Redis cache clusters version to 3.2.6 will get you all the improvements that come with the Redis engine version 3 (data partitioning, geospatial indexing, online cluster resizing, replica scaling, etc) plus the ones added by AWS such as support for newer cache node types, in-transit and at-rest encryption, and support for HIPAA compliance. For Memcached cache clusters, upgrading the engine version to 1.4.34 will add several bug fixes, systemd service hardening, improved support for large items over 1MB and the ability to dynamically increase the amount of memory available to the engine without having to restart the cache cluster.',
      serviceName: 'AmazonElastiCache',
      recommendations: [
        'Ensure that your Amazon ElastiCache clusters are using the stable latest version of Redis/Memcached cache engine in order to adhere to AWS cloud best practices, benefit from better security by having the most recent vulnerability patches, receive the latest Redis and Memcached software features, and get the latest performance optimizations.',
      ],
      mitigations: [
        `**Terraform configuration file (.tf):**
\`\`\`
        resource "aws_elasticache_cluster" "memcached-cache-cluster" {

            cluster_id           = "cc-memcached-cluster"
            engine               = "memcached"
            node_type            = "cache.t2.micro"
            num_cache_nodes      = 2
            availability_zone    = "us-east-1b"
            parameter_group_name = "default.memcached1.6"
            security_group_ids   = ["sg-0abcd1234abcd1234"]

            # Upgrade Memcached Cache Engine to Latest Supported Version
            engine_version       = "1.6.6"
            apply_immediately    = true

        }
\`\`\`
`,
        `**AWS CLI command to upgrade ElastiCache engine version:**
\`\`\`
        aws elasticache modify-cache-cluster
        --region {{region}}
        --cache-cluster-id {{recommendation.cluster_id}}
        --engine-version 1.6.6
        --apply-immediately
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/advisor/advisor-security-recommendations'],
    },
    aws_elasticache_instance_generation: {
      title: 'Elasticache Instance Generation should be latest',
      description:
        'Using the latest generation of Amazon ElastiCache cluster nodes instead of the previous generation nodes has tangible benefits such as better hardware performance (more computing capacity and faster CPUs, memory optimization, superior I/O, and higher network throughput), better support for the newest Redis/Memcached engine versions, and lower costs for CPU, memory, and storage.',
      serviceName: 'AmazonElastiCache',
      recommendations: [
        `Ensure that all the Amazon ElastiCache cache clusters provisioned in your AWS account are using the latest generation of cache node types in order to get the best performance with lower costs. If you are using cache nodes from the previous generation, Trend Micro Cloud One™ – Conformity strongly recommends that you upgrade your nodes with their latest generation equivalents.`,
      ],
      mitigations: [
        `**AWS CLI command to upgrade ElastiCache instance generation:**
\`\`\`
        aws elasticache modify-replication-group
        --region {{region}}
        --replication-group-id {{recommendation.cluster_id}}
        --cache-node-type cache.r5.large
        --apply-immediately
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/CacheNodes.SupportedTypes.html'],
    },
    aws_ec2_orphaned_volume: {
      title: 'EBS Volumes Should Not Be Orphaned',
      description:
        'Any Amazon EBS volume provisioned in your AWS cloud account adds charges to your monthly bill, regardless of whether it is in use. If you have Amazon EBS volumes that are not attached to EC2 instances and their data is no longer needed, consider deleting these volumes. Removing unattached/orphaned Amazon EBS volumes from your AWS account will help you to avoid unexpected charges on your AWS bill and halt access to any sensitive data available on these volumes.',
      serviceName: 'AmazonEC2',
      recommendations: [
        `Identify unused (unattached) Amazon Elastic Block Store (EBS) volumes available within your AWS cloud account and delete these volumes in order to lower the cost of your AWS bill and reduce the risk of confidential and sensitive data leaks.`,
      ],
      mitigations: [
        `**AWS CLI command to delete orphaned EBS volume:**
        - Create Snapshot
\`\`\`
        aws ec2 create-snapshot --region {{region}} --volume-id {{recommendation.volume_id}}        
\`\`\`

        - Delete Volume
\`\`\`
        aws ec2 delete-volume
        --region {{region}}
        --volume-id {{recommendation.volume_id}}
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ebs-deleting-volume.html'],
    },
    aws_ec2_ebs_generation_upgrade: {
      title: 'Use latest EBS Volume Generation',
      description:
        'For same amount of storage space, gp3 is 20 percent more cost-effective than gp2 volumes. It’s also interesting to consider what a maximum performance and maximum throughput volume would cost for both gp2 and gp3',
      serviceName: 'AmazonEC2',
      recommendations: ['Migrate EBS volumes from GP2 to GP3 to save up to 20% on costs.'],
      mitigations: [
        `**AWS CLI command to upgrade EBS volume generation:**
\`\`\`
        aws ec2 modify-volume --volume-type {{recommendation.recommendded_volume_type}} --volume-id {{recommendation.volume_id}}
\`\`\`
`,
      ],
      references: ['https://aws.amazon.com/blogs/storage/migrate-your-amazon-ebs-volumes-from-gp2-to-gp3-and-save-up-to-20-on-costs/'],
    },
    aws_ec2_instance_start: {
      title: '',
      description: '',
      serviceName: 'AmazonEC2',
      recommendations: [],
      mitigations: [],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Stop_Start.html'],
    },
    aws_rds_instance_generation: {
      title: 'RDS Instance Generation Upgrade',
      description:
        'Using the latest generation of Amazon RDS database instances instead of the previous generation instances has tangible benefits such as better hardware performance (more computing capacity and faster CPUs, memory optimization and higher network throughput), better support for latest database engines versions, and lower costs for memory and storage',
      serviceName: 'AmazonRDS',
      recommendations: [
        `Ensure that all the Amazon RDS databases instances provisioned within your AWS account are using the latest generation of instance classes in order to get the best performance with lower costs. If you are using database instances from the previous generation, Trend Micro Cloud One™ – Conformity strongly recommends that you upgrade your instances with their latest generation equivalents.`,
      ],
      mitigations: [
        `**AWS CLI command to upgrade RDS instance generation:**
\`\`\`
        aws rds modify-db-instance --region {{region}} --db-instance-identifier {{recommendation.instance_id}} --db-instance-class db.t3.medium --apply-immediately
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Concepts.DBInstanceClass.html'],
    },
    // --- AWS Fargate/ECS ---
    aws_fargate_latest_platform_version: {
      title: 'Fargate Task Uses Latest Platform Version',
      description:
        'AWS Fargate platform versions control the runtime environment for tasks. Older platform versions may lack security patches, performance improvements, and new features. Running on the latest platform version ensures you benefit from the most recent improvements.',
      serviceName: 'AWSFargate',
      recommendations: ['Ensure all Fargate tasks use the LATEST platform version to benefit from security patches and performance improvements.'],
      mitigations: [
        `Update the Fargate service or task definition to use platform version LATEST:
\`\`\`
aws ecs update-service --cluster {{recommendation.cluster_name}} --service {{recommendation.service_name}} --platform-version LATEST
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/platform_versions.html'],
    },
    aws_ecs_fargate_latest_platform_version: {
      title: 'ECS Fargate Service Uses Latest Platform Version',
      description:
        'ECS Fargate services should run on the latest platform version to receive security fixes, kernel updates, and new features. Using outdated platform versions may expose your workloads to known vulnerabilities.',
      serviceName: 'AmazonECS',
      recommendations: ['Update ECS Fargate services to use the LATEST platform version.'],
      mitigations: [
        `Update the service platform version:
\`\`\`
aws ecs update-service --cluster {{recommendation.cluster_name}} --service {{recommendation.service_name}} --platform-version LATEST
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/platform_versions.html'],
    },
    // --- AWS Elastic Beanstalk ---
    aws_elasticbeanstalk_outdated_platform: {
      title: 'Elastic Beanstalk Uses Outdated Platform',
      description:
        'AWS Elastic Beanstalk environments running on outdated or retired platform versions may miss critical security patches, bug fixes, and performance improvements. AWS regularly retires older platform branches.',
      serviceName: 'AWSElasticBeanstalk',
      recommendations: ['Upgrade Elastic Beanstalk environments to the latest supported platform version.'],
      mitigations: [
        `Update the environment platform version:
\`\`\`
aws elasticbeanstalk update-environment --environment-name {{resource_name}} --solution-stack-name "64bit Amazon Linux 2 v5.x.x running Node.js 18"
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/elasticbeanstalk/latest/dg/using-features.platform.upgrade.html'],
    },
    aws_elasticbeanstalk_unhealthy: {
      title: 'Elastic Beanstalk Environment Health Degraded',
      description:
        'An Elastic Beanstalk environment with degraded or severe health status indicates issues with the underlying instances, deployment, or application. This can affect availability and performance.',
      serviceName: 'AWSElasticBeanstalk',
      recommendations: ['Investigate and resolve health issues in degraded Elastic Beanstalk environments.'],
      mitigations: [
        `Check environment health:
\`\`\`
aws elasticbeanstalk describe-environment-health --environment-name {{resource_name}} --attribute-names All
\`\`\`
Review recent events and instance health to identify root cause.`,
      ],
      references: ['https://docs.aws.amazon.com/elasticbeanstalk/latest/dg/health-enhanced.html'],
    },
    // --- AWS Redshift ---
    aws_redshift_cluster_version: {
      title: 'Redshift Cluster Version Upgrade',
      description:
        'Amazon Redshift regularly releases new cluster versions with performance improvements, security patches, and new features. Running an outdated version may expose you to known vulnerabilities and miss optimizations.',
      serviceName: 'AmazonRedshift',
      recommendations: ['Ensure Redshift clusters are running the latest available version and that automatic version upgrades are enabled.'],
      mitigations: [
        `Enable automatic version upgrades:
\`\`\`
aws redshift modify-cluster --cluster-identifier {{recommendation.cluster_id}} --allow-version-upgrade
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/redshift/latest/mgmt/cluster-versions.html'],
    },
    // --- GCP ---
    gcp_gke_old_cluster: {
      title: 'GKE Cluster Running Outdated Kubernetes Version',
      description:
        'GKE clusters running older Kubernetes versions miss security patches, bug fixes, and new features. Google regularly deprecates older minor versions and eventually removes support.',
      serviceName: 'GKE',
      recommendations: ['Upgrade GKE clusters to a supported and recent Kubernetes version.'],
      mitigations: [
        `Upgrade the cluster control plane and node pools:
\`\`\`
gcloud container clusters upgrade {{resource_name}} --master --cluster-version=LATEST_VERSION
gcloud container node-pools update my-pool --cluster={{resource_name}} --node-version=LATEST_VERSION
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/kubernetes-engine/docs/concepts/release-channels'],
    },
    gcp_compute_old_instance: {
      title: 'GCE Instance Using Outdated Machine Type',
      description:
        'Google Compute Engine regularly introduces new machine type families (e.g., N2, E2, C3) with better price-performance ratios. Running instances on older machine types (N1) may cost more for less performance.',
      serviceName: 'ComputeEngine',
      recommendations: ['Migrate Compute Engine instances to the latest machine type generation for better performance and lower cost.'],
      mitigations: [
        `Stop the instance, change machine type, and restart:
\`\`\`
gcloud compute instances stop {{resource_name}} --zone=us-central1-a
gcloud compute instances set-machine-type {{resource_name}} --machine-type=e2-standard-4 --zone=us-central1-a
gcloud compute instances start {{resource_name}} --zone=us-central1-a
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/compute/docs/instances/create-start-instance'],
    },
    gcp_sql_old_instance: {
      title: 'Cloud SQL Instance Running Outdated Database Version',
      description:
        'Cloud SQL instances running older database engine versions (e.g., MySQL 5.7, PostgreSQL 12) may not receive security updates and lack newer features. Google eventually ends support for older versions.',
      serviceName: 'CloudSQL',
      recommendations: ['Upgrade Cloud SQL instances to a supported and recent database engine version.'],
      mitigations: [
        `Upgrade the database version (requires downtime):
\`\`\`
gcloud sql instances patch {{resource_name}} --database-version=POSTGRES_15
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/sql/docs/mysql/db-versions'],
    },
    gcp_storage_old_bucket: {
      title: 'GCS Bucket Using Legacy Storage Class',
      description:
        'Google Cloud Storage buckets using legacy storage classes (e.g., Multi-Regional, Regional) should be migrated to the newer Standard, Nearline, Coldline, or Archive classes for better pricing and features.',
      serviceName: 'CloudStorage',
      recommendations: ['Update GCS bucket storage class from legacy to a current storage class.'],
      mitigations: [
        `Update bucket storage class:
\`\`\`
gcloud storage buckets update gs://{{resource_name}} --default-storage-class=STANDARD
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/storage/docs/storage-classes'],
    },
    // --- Azure ---
    azure_aks_old_kubernetes_version: {
      title: 'AKS Cluster Running Outdated Kubernetes Version',
      description:
        'Azure Kubernetes Service clusters running outdated Kubernetes versions miss security patches and new features. Azure supports only a limited number of minor versions at any given time.',
      serviceName: 'AKS',
      recommendations: ['Upgrade AKS clusters to a supported and recent Kubernetes version.'],
      mitigations: [
        `Upgrade the AKS cluster:
\`\`\`
az aks upgrade --resource-group {{resource_group}} --name {{resource_name}} --kubernetes-version 1.28.0
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/aks/supported-kubernetes-versions'],
    },
    azure_function_old_runtime: {
      title: 'Azure Function Using Outdated Runtime Version',
      description:
        'Azure Functions running on deprecated or outdated runtime versions may lack security patches and new features. Microsoft regularly retires older runtime versions.',
      serviceName: 'AzureFunctions',
      recommendations: ['Upgrade Azure Functions to the latest supported runtime version.'],
      mitigations: [
        `Update the function app runtime version:
\`\`\`
az functionapp config set --name {{resource_name}} --resource-group {{resource_group}} --linux-fx-version "DOTNET|8.0"
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/azure-functions/functions-versions'],
    },
    azure_managed_disk_sku_upgrade: {
      title: 'Azure Managed Disk SKU Upgrade Available',
      description:
        'Azure Managed Disks have newer SKU options (e.g., Premium SSD v2) that offer better performance-per-cost. Standard HDD disks used for production workloads may benefit from an upgrade.',
      serviceName: 'AzureManagedDisks',
      recommendations: ['Consider upgrading managed disk SKU to a newer generation for better performance.'],
      mitigations: [
        `Update disk SKU:
\`\`\`
az disk update --name {{resource_name}} --resource-group {{resource_group}} --sku Premium_LRS
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/disks-types'],
    },
    azure_disk_premium_ssd_v2_upgrade: {
      title: 'Azure Disk Eligible for Premium SSD v2 Upgrade',
      description:
        'Premium SSD v2 offers sub-millisecond latency with independently configurable IOPS and throughput. Disks currently using Premium SSD (v1) may benefit from upgrading to v2 for better cost-performance.',
      serviceName: 'AzureManagedDisks',
      recommendations: ['Evaluate whether Premium SSD v2 is a better fit for workloads currently using Premium SSD v1.'],
      mitigations: ['Premium SSD v2 migration requires creating a new disk and copying data. Refer to Azure documentation for migration steps.'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/disks-types'],
    },
    azure_expressroute_enable_standard_sku: {
      title: 'ExpressRoute Circuit Should Use Standard or Premium SKU',
      description:
        'Azure ExpressRoute circuits using the Basic or Local SKU may have limited features. Standard or Premium SKUs provide better routing options, more virtual network links, and global reach.',
      serviceName: 'AzureExpressRoute',
      recommendations: ['Evaluate whether upgrading the ExpressRoute circuit SKU would benefit your connectivity requirements.'],
      mitigations: [
        `Update ExpressRoute SKU:
\`\`\`
az network express-route update --name {{resource_name}} --resource-group {{resource_group}} --sku-tier Standard
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/expressroute/expressroute-about-virtual-network-gateways'],
    },
    azure_vm_generation_upgrade: {
      title: 'Azure VM Using Older Generation',
      description:
        'Azure regularly introduces new VM series with better price-performance. VMs running on older series (e.g., Dv2) can be upgraded to newer series (e.g., Dv5) for improved performance at similar or lower cost.',
      serviceName: 'AzureVMs',
      recommendations: ['Upgrade Azure VMs to the latest generation VM series for better performance and cost efficiency.'],
      mitigations: [
        `Resize the VM:
\`\`\`
az vm resize --resource-group {{resource_group}} --name {{resource_name}} --size Standard_D4s_v5
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/generation-2'],
    },
    azure_logic_app_outdated_workflow: {
      title: 'Azure Logic App Workflow Is Outdated',
      description: 'The Logic App workflow version is outdated and should be updated.',
      serviceName: 'LogicApps',
      recommendations: ['The Logic App workflow version is outdated and should be updated.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/logic-apps/logic-apps-overview'],
    },
    gcp_compute_generation_upgrade: {
      title: 'GCP Compute Instance Should Be Upgraded to Newer Generation',
      description:
        'This GCP Compute Engine instance is running on an older machine type generation. Newer generation machine types offer better price-performance ratios with improved processors and hardware capabilities.',
      serviceName: 'ComputeEngine',
      recommendations: [
        'Upgrade the instance to a newer machine type generation (e.g., from N1 to N2/N2D, or E2) for improved performance and cost efficiency.',
      ],
      mitigations: [
        `Stop the instance and change the machine type:
\`\`\`
gcloud compute instances stop {{instance_name}} \\
  --zone={{zone}}

gcloud compute instances set-machine-type {{instance_name}} \\
  --zone={{zone}} \\
  --machine-type=n2-standard-4

gcloud compute instances start {{instance_name}} \\
  --zone={{zone}}
\`\`\``,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://cloud.google.com/compute/docs/machine-resource'],
    },
    vm_generation_upgrade: {
      title: 'VM Generation Upgrade Available',
      description: 'This virtual machine is running on an older hardware generation. Newer generations typically offer better price-performance.',
      recommendations: ['Upgrade to the latest instance generation for improved performance and cost efficiency.'],
    },
  },
  RightSizing: {
    aws_rds_underutilized: {
      title: 'Underutilized RDS Instance',
      description: `Identify any Amazon RDS database instances that appear to be underutilized and downsize (resize) them to help lower the cost of your monthly AWS bill. By default, an RDS database instance is considered "underutilized" when meets the following criteria:
        The average CPU utilization has been less than 60% for the last 7 days.
        The total number of ReadIOPS and WriteIOPS recorded per day for the last 7 days has been less than 100 on average.
        The AWS CloudWatch metrics utilized to detect underused RDS instances are:
        CPUUtilization - the percentage of CPU utilization (Units: Percent).
        ReadIOPS and WriteIOPS - the average number of disk I/O (Input/Output) operations per second (Units: Count/Second).
        Note: You can change the default threshold values for this rule on the Cloud Conformity console and set your own values for CPU utilization, and the total number of ReadIOPS and WriteIOPS to configure the underuse level for your RDS instances.`,
      serviceName: 'AmazonRDS',
      recommendations: [
        `Downsizing underused RDS database instances represents a good strategy for optimizing your monthly AWS costs. For example, downgrading a db.m3.large RDS MySQL database instance to a db.m3.medium instance due to CPU and IOPS underuse, you can save roughly $70 per month (as of March 2017).`,
      ],
      mitigations: [
        `**AWS CLI command to upgrade RDS instance generation:**
\`\`\`
        aws rds modify-db-instance --region {{region}} --db-instance-identifier {{recommendation.instance_id}} --db-instance-class db.t3.medium --apply-immediately
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/MonitoringOverview.html'],
    },
    aws_rds_overutilized: {
      title: 'Overutilized AWS RDS Instances',
      description: `Identify any Amazon RDS database instances that appear to be overutilized and upgrade (upsize) them to help handle better the database workload and improve the response time. By default, an RDS database instance is considered "overutilized" when meets the following criteria:
      The daily average CPU utilization has been more than 90% for the last 7 days.
      - The AWS CloudWatch metrics utilized to detect overused RDS instances are:
      CPUUtilization - the percentage of CPU utilization (Units: Percent).
      Note: You can change the default threshold values for this rule on the Cloud Conformity console and set your own values for CPU utilization to configure the overuse level for your RDS instances.`,
      serviceName: 'AmazonRDS',
      recommendations: [
        `Overutilized AWS RDS instances could indicate that the databases running on these servers do not have enough hardware resources to perform optimally. Upgrading (upsizing) overutilized RDS instances to meet the load needs will improve directly the health and success of your databases (and their applications).`,
      ],
      mitigations: [
        `**AWS CLI command to upgrade RDS instance generation:**
\`\`\`
  aws rds modify-db-instance --region {{region}} --db-instance-identifier {{recommendation.instance_id}} --db-instance-class db.t3.medium --apply-immediately
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/MonitoringOverview.html'],
    },
    aws_rds_alternate_instances: {
      title: 'RDS Alternate Instances',
      description:
        'Alternate instances represent a good candidate for reducing your monthly AWS costs. Regularly checking your AWS RDS instances for the number of database connections performed will help you efficiently detect and remove any alternate RDS resources from your AWS account in order to avoid accumulating unnecessary charges.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `Ensure that any Amazon RDS database instances that appear to be alternate are deleted to help lower the cost of your monthly AWS bill. By default, an RDS instance is considered 'alternate' when meets the following criteria (to declare the instance 'alternate' both conditions must be true):
          - memory and cpu configuration should be same
          - RDS engine version should be same
        `,
      ],
      mitigations: [
        `**AWS CLI command to upgrade RDS instance generation:**
\`\`\`
        aws rds modify-db-instance --region {{region}} --db-instance-identifier {{recommendation.instance_id}} --db-instance-class db.t3.medium --apply-immediately
\`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Concepts.DBInstanceClass.html'],
      drilldownInvestigation: function (recommendation: any) {
        const tableData: any[] = [];
        recommendation?.recommendation?.alternate_instances?.forEach((instance: any) => {
          tableData.push([
            { text: instance?.product?.attributes?.instanceType },
            { text: instance?.product?.attributes?.physicalProcessor },
            {
              text: Number(
                instance?.terms?.OnDemand[Object.keys(instance?.terms?.OnDemand)[0]]?.priceDimensions[
                  Object.keys(instance?.terms?.OnDemand[Object.keys(instance?.terms?.OnDemand)[0]]?.priceDimensions)[0]
                ]?.pricePerUnit?.USD
              ).toFixed(3),
            },
          ]);
        });
        return (
          <div>
            <h3>
              Alternate Instances(Current Price -{' '}
              {Number(
                recommendation?.cloud_resourse?.meta?.InstanceTypeDetails?.terms?.OnDemand[
                  Object.keys(recommendation?.cloud_resourse?.meta?.InstanceTypeDetails?.terms?.OnDemand)[0]
                ]?.priceDimensions[
                  Object.keys(
                    recommendation?.cloud_resourse?.meta?.InstanceTypeDetails?.terms?.OnDemand[
                      Object.keys(recommendation?.cloud_resourse?.meta?.InstanceTypeDetails?.terms?.OnDemand)[0]
                    ]?.priceDimensions
                  )[0]
                ]?.pricePerUnit?.USD
              ).toFixed(3)}
              )
            </h3>
            <CustomTable rowsPerPage={100} headers={['Instance Type', 'Processor', 'Cost(Hrs)']} tableData={tableData} />
          </div>
        );
      },
    },
    aws_rds_free_storage_space: {
      title: 'RDS Storage Space is 10pct Free',
      description:
        'Low disk space will often lead to instability and slowdowns. Detecting RDS database instances that run low on disk space is crucial when these instances are used in production by latency sensitive applications as this can help you take immediate actions and expand the storage space in order to maintain an optimal response time.',
      serviceName: 'AmazonRDS',
      recommendations: [
        'Identify any Amazon RDS database instances that appear to run low on disk space and scale them up to alleviate any problems triggered by insufficient disk space and improve their I/O performance. The default threshold value set for the amount of free storage space is 10% as any value below this could have a serious impact on your database stability and performance. For example, if the free storage space becomes dangerously low, basic operations like connecting to the database will not be possible anymore.',
      ],
      mitigations: [
        `**AWS CLI command to scale up RDS instance storage space:**
\`\`\`
    aws rds modify-db-instance --region {{region}} --db-instance-identifier {{recommendation.instance_id}} --allocated-storage 100 --apply-immediately
\`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_PerfInsights.html'],
    },
    aws_rds_idle_instance: {
      title: 'Idle RDS Instance',
      serviceName: 'AmazonRDS',
      description: `Idle RDS instances represent a good candidate for reducing your monthly AWS costs. Regularly checking your AWS RDS instances for the number of database connections performed will help you efficiently detect and remove any idle RDS resources from your AWS account in order to avoid accumulating unnecessary charges.
  
  Note 1: Backing up your RDS databases before termination is highly recommended because once these instances are deleted, all their automated backups (snapshots) will be permanently lost.
  Note 2: Knowing the role and the owner of an AWS RDS instance before you take the decision to remove it from your account is very important. For this rule Cloud Conformity assumes that your RDS instances are tagged with 'Role' and 'Owner' tags which provide visibility into their usage profile and help you decide whether it's safe or not to terminate these resources.
  Note 3: You can change the default threshold for this rule on the Cloud Conformity console and set your own values for the number of database connections, and the total number of ReadIOPS and WriteIOPS for each condition in order to configure the instances idleness.
  Note 4: If the RDS database instance selected for the checkup is needed within your application stack, you can suppress (disable) the conformity rule check for the instance from the Cloud Conformity console.`,
      recommendations: [
        `
Identify any Amazon RDS database instances that appear to be idle and delete them to help lower the cost of your monthly AWS bill. By default, an RDS instance is considered 'idle' when meets the following criteria (to declare the instance 'idle' both conditions must be true):

The average number of database connections has been less than 1 for the last 7 days.
The total number of database ReadIOPS and WriteIOPS recorded per day for the last 7 days has been less than 20 on average.
The AWS CloudWatch metrics used to detect idle RDS instances are:

DatabaseConnections - the number of RDS database connections in use (Units: Count).
ReadIOPS and WriteIOPS - the average number of disk I/O (Input/Output) operations per second (Units: Count/Second).        
        `,
      ],
      mitigations: [
        `Delete the identified idle Amazon RDS database instances to reduce your monthly AWS costs. Before you delete an RDS instance, ensure that you have backed up the database to avoid losing any important data. To delete an RDS instance, use the AWS Management Console, AWS CLI or AWS SDKs.
\`\`\`
aws rds delete-db-instance --region {{region}} --db-instance-identifier {{recommendation.instance_id}} --no-skip-final-snapshot --final-db-snapshot-identifier {{recommendation.instance_id}}-final-snapshot
\`\`\`
 `,
      ],
      references: ['https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/MonitoringOverview.html'],
    },
    aws_ec2_idle_instance: {
      title: 'Idle EC2 Instance',
      description:
        'Idle instances represent a good candidate to reduce your Amazon EC2 service costs and avoid accumulating unnecessary Amazon EC2 charges.',
      serviceName: 'AmazonRDS',
      recommendations: [
        `
        Identify any Amazon EC2 instances that appear to be idle and stop or terminate them to help lower the cost of your AWS bill. By default, an Amazon EC2 instance is considered "idle" when meets the following criteria (to declare the instance "idle" both conditions must be true):
        - The average CPU Utilization has been less than 2% for the last 7 days.
        - The average Network I/O has been less than 5 MB for the last 7 days.
        `,
      ],
      mitigations: [
        `**AWS CLI command to stop or terminate idle EC2 instance:**
        - Stop Instance
\`\`\`
        aws ec2 stop-instances --region {{region}} --instance-ids {{recommendation.instance_id}}
\`\`\`

        - Terminate Instance
\`\`\`
        aws ec2 terminate-instances --region {{region}} --instance-ids {{recommendation.instance_id}}
\`\`\`
        
        `,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/monitoring-system-instance-status-check.html'],
    },
    aws_ec2_alternate_instances: {
      title: 'EC2 Alternate Instances',
      description:
        'Alternate instances represent a good candidate for reducing your monthly AWS costs. Regularly checking your AWS EC2 instances for the number of database connections performed will help you efficiently detect and remove any alternate EC2 resources from your AWS account in order to avoid accumulating unnecessary charges.',
      serviceName: 'AmazonEC2',
      recommendations: [
        `Ensure that any Amazon EC2 instances that appear to be alternate are deleted to help lower the cost of your monthly AWS bill. By default, an EC2 instance is considered 'alternate' when meets the following criteria (to declare the instance 'alternate' both conditions must be true):
          - memory and cpu configuration should be same
        `,
      ],
      mitigations: [
        `**AWS CLI command to upgrade EC2 instance generation:**

        - Stop instance
\`\`\`
          aws ec2 stop-instances --instance-ids "$INSTANCE_ID"
\`\`\`

        - Modify Instance Type

\`\`\`
          aws ec2 modify-instance-attribute --instance-id "$INSTANCE_ID" --instance-type {"Value":"$REQUESTED_TYPE"}
\`\`\`

        - Start Instance

\`\`\`
          aws ec2 start-instances --instance-ids "$INSTANCE_ID"
\`\`\`

`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-resize.html'],
      drilldownInvestigation: function (recommendation: any) {
        console.log(recommendation);
        const tableData: any[] = [];
        recommendation?.recommendation?.alternate_instances?.forEach((instance: any) => {
          tableData.push([{ text: instance?.instanceType }, { text: instance?.price }]);
        });
        return (
          <div>
            <h3>Alternate Instances (Current Price - {Number(recommendation?.cloud_resourse?.meta?.InstanceTypeDetails?.Price).toFixed(3)})</h3>
            <CustomTable rowsPerPage={100} headers={['Instance Type', 'Cost(Hrs)']} tableData={tableData} />
          </div>
        );
      },
    },
    aws_ec2_underutilized: {
      title: 'Underutilized EC2 Instance',
      description: `Identify any Amazon EC2 instances that appear to be underutilized and downsize (resize) them to help lower the cost of your AWS bill. By default, an Amazon EC2 instance is considered "underutilized" when matches the following criteria (to declare the instance "underutilized" both conditions must be met):
    The average CPU utilization has been less than 60% for the last 7 days.
    The average memory utilization has been less than 60% for the last 7 days. By default, Amazon CloudWatch can't record an EC2 instance memory utilization because the necessary metric cannot be implemented at the hypervisor level, therefore to be able to report the memory utilization using CloudWatch you need to install an agent on the instance that you want to monitor and create a custom metric (we'll name it EC2MemoryUtilization) on the Amazon CloudWatch console. The instructions required for installing the monitoring agent, based on the Operating System (OS) used by the instance, are available at this URL.`,
      serviceName: 'AmazonEC2',
      recommendations: [
        `Downsizing underutilized Amazon EC2 instances to meet the capacity needs at the lowest cost represents an efficient strategy to reduce your AWS cloud costs. For example, resizing a c4.xlarge-type instance provisioned in the US-East (N. Virginia) region to a c4.large-type instance due to CPU and memory underuse, you can roughly save $72 per month.`,
      ],
      mitigations: [
        `**AWS CLI command to upgrade E2 instance generation:**
\`\`\`
          aws ec2 modify-instance-attribute --region {{region}} --instance-id {{recommendation.instance_id}} --instance-type {"Value": "{{recommendation.recommended_instance_type}}"} 
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instance-types.html'],
    },
    aws_ec2_overutilized: {
      title: 'Overutilized AWS EC2 Instances',
      description: `Identify any Amazon EC2 instances that appear to be overutilized and upgrade (resize) them in order to help your EC2-hosted applications to handle better the workload and improve the response time. By default, an Amazon EC2 instance is considered "overutilized" when matches the following criteria:
        The average CPU utilization has been more than 90% for the last 7 days.
        The average memory utilization has been more than 90% for the last 7 days. By default, Amazon CloudWatch can't record an EC2 instance memory utilization because the necessary metric cannot be implemented at the hypervisor level, therefore to be able to report the memory utilization using CloudWatch you need to install an agent (PERL script) on the instance that you want to monitor and create a custom metric (we'll name it EC2MemoryUtilization) on the CloudWatch console. The instructions required for installing the monitoring agent, based on the Operating System used by instance, are available at this`,
      serviceName: 'AmazonEC2',
      recommendations: [
        `Overutilized Amazon EC2 instances could indicate that the applications running on these machines do not have enough hardware resources to perform optimally. Upgrading (upsizing) overutilized Amazon EC2 instances to meet your load needs will improve directly the health and success of your applications, resulting in a more stable environment and a faster response time.`,
      ],
      mitigations: [
        `**AWS CLI command to upgrade Ec2 instance generation:**
\`\`\` 
  aws rds modify-db-instance --region {{region}} --db-instance-identifier {{recommendation.instance_id}} --db-instance-class db.t3.medium --apply-immediately
\`\`\`
        `,
      ],
      references: ['https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instance-types.html'],
    },
    // --- AWS Fargate/ECS RightSizing ---
    aws_fargate_service_underutilized: {
      title: 'Fargate Service Underutilized',
      description:
        'This Fargate service has low average CPU and memory utilization over the past 7 days. The allocated vCPU and memory may be larger than needed, resulting in unnecessary costs.',
      serviceName: 'AWSFargate',
      recommendations: ['Reduce the vCPU and memory allocation in the Fargate task definition to match actual utilization.'],
      mitigations: [
        `Update the task definition with lower resource values and update the service:
\`\`\`
aws ecs register-task-definition --family {{recommendation.task_definition_arn}} --cpu 256 --memory 512 ...
aws ecs update-service --cluster {{recommendation.cluster_name}} --service {{recommendation.service_name}} --task-definition {{recommendation.task_definition_arn}}
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/cloudwatch-metrics.html'],
    },
    aws_fargate_service_overutilized: {
      title: 'Fargate Service Overutilized',
      description:
        'This Fargate service has high average CPU or memory utilization over the past 7 days. The allocated resources may be insufficient, risking throttling or OOM kills.',
      serviceName: 'AWSFargate',
      recommendations: ['Increase the vCPU and/or memory allocation in the Fargate task definition to handle the workload.'],
      mitigations: [
        `Update the task definition with higher resource values and update the service:
\`\`\`
aws ecs register-task-definition --family {{recommendation.task_definition_arn}} --cpu 1024 --memory 2048 ...
aws ecs update-service --cluster {{recommendation.cluster_name}} --service {{recommendation.service_name}} --task-definition {{recommendation.task_definition_arn}}
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/cloudwatch-metrics.html'],
    },
    aws_ecs_fargate_service_underutilized: {
      title: 'ECS Fargate Service Underutilized',
      description:
        'This ECS Fargate service has consistently low CPU and memory utilization. Consider reducing task resource allocations to save costs.',
      serviceName: 'AmazonECS',
      recommendations: ['Right-size ECS Fargate task definitions by reducing CPU and memory to match actual usage patterns.'],
      mitigations: [
        'Update the task definition with reduced CPU and memory values, then update the service to use the new task definition revision.',
      ],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/cloudwatch-metrics.html'],
    },
    aws_ecs_fargate_service_overutilized: {
      title: 'ECS Fargate Service Overutilized',
      description:
        'This ECS Fargate service has consistently high CPU or memory utilization. Insufficient resources may cause performance degradation or task failures.',
      serviceName: 'AmazonECS',
      recommendations: ['Increase the CPU and/or memory allocation in the ECS Fargate task definition.'],
      mitigations: ['Register a new task definition revision with increased CPU and memory, then update the service.'],
      references: ['https://docs.aws.amazon.com/AmazonECS/latest/developerguide/cloudwatch-metrics.html'],
    },
    // --- AWS ELB ---
    aws_elb_unused: {
      title: 'Unused Elastic Load Balancer',
      description:
        'An Elastic Load Balancer with no registered targets or very low request count is incurring costs without providing value. Unused load balancers should be identified and removed.',
      serviceName: 'AmazonELB',
      recommendations: ['Delete unused Elastic Load Balancers that have no registered targets or negligible traffic.'],
      mitigations: [
        `Delete the unused load balancer:
\`\`\`
aws elbv2 delete-load-balancer --load-balancer-arn arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-alb/1234567890
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/elasticloadbalancing/latest/classic/elb-deregister-register-instances.html'],
    },
    // --- AWS EC2 Stopped ---
    // --- AWS ElastiCache ---
    aws_elasticache_idle_instance: {
      title: 'Idle ElastiCache Instance',
      description:
        'An ElastiCache cluster with very few or no connections over the past 7 days may be idle. Idle clusters incur costs without providing value.',
      serviceName: 'AmazonElastiCache',
      recommendations: ['Delete idle ElastiCache clusters that are no longer in use to reduce costs.'],
      mitigations: [
        `Delete the idle cluster:
\`\`\`
aws elasticache delete-cache-cluster --cache-cluster-id {{recommendation.cluster_id}} --final-snapshot-identifier {{recommendation.cluster_id}}-final-snapshot
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/monitoring-elasticache.html'],
    },
    aws_elasticache_oversized: {
      title: 'ElastiCache Cluster Is Oversized',
      description:
        'This ElastiCache cluster is using significantly less memory than allocated (< 30% utilization), with high cache hit rates (> 95%) and no evictions. The cluster can be downsized to a smaller node type to reduce costs while maintaining performance.',
      serviceName: 'AmazonElastiCache',
      recommendations: [
        'Downsize to a smaller cache node type when memory utilization is consistently below 30% with zero evictions and cache hit rate above 95%.',
        'Monitor BytesUsedForCache, Evictions, and CacheHitRate metrics for at least 7 days before downsizing.',
        'Plan the change during a maintenance window to minimize impact on applications.',
      ],
      mitigations: [
        `Downsize Redis cluster:
\`\`\`
aws elasticache modify-cache-cluster \\
  --cache-cluster-id {{recommendation.cluster_id}} \\
  --cache-node-type {{recommendation.recommended_node_type}} \\
  --apply-immediately
\`\`\`

**Terraform configuration:**
\`\`\`hcl
resource "aws_elasticache_cluster" "example" {
  cluster_id           = "{{recommendation.cluster_id}}"
  engine               = "redis"
  node_type            = "{{recommendation.recommended_node_type}}"
  num_cache_nodes      = 1
  parameter_group_name = "default.redis7"
}
\`\`\`
`,
      ],
      references: [
        'https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/Scaling.html',
        'https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/CacheMetrics.WhichShouldIMonitor.html',
      ],
    },
    aws_elasticache_undersized: {
      title: 'ElastiCache Cluster Is Undersized',
      description:
        'This ElastiCache cluster is experiencing memory pressure indicated by evictions and low cache hit rates (< 80%). The cluster needs to be upsized to a larger node type to improve performance and prevent data eviction.',
      serviceName: 'AmazonElastiCache',
      recommendations: [
        'Upsize to a larger cache node type when experiencing sustained evictions and cache hit rate below 80%.',
        'Evictions indicate memory pressure and can significantly degrade application performance.',
        'Monitor performance metrics before and after upsizing to validate the improvement.',
      ],
      mitigations: [
        `Upsize Redis cluster:
\`\`\`
aws elasticache modify-cache-cluster \\
  --cache-cluster-id {{recommendation.cluster_id}} \\
  --cache-node-type {{recommendation.recommended_node_type}} \\
  --apply-immediately
\`\`\`

**Terraform configuration:**
\`\`\`hcl
resource "aws_elasticache_cluster" "example" {
  cluster_id           = "{{recommendation.cluster_id}}"
  engine               = "redis"
  node_type            = "{{recommendation.recommended_node_type}}"
  num_cache_nodes      = 1
  parameter_group_name = "default.redis7"
}
\`\`\`
`,
      ],
      references: [
        'https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/Scaling.html',
        'https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/CacheMetrics.WhichShouldIMonitor.html',
      ],
    },
    aws_elasticache_low_hit_rate: {
      title: 'ElastiCache Cluster Has Low Cache Hit Rate',
      description:
        'This ElastiCache cluster has a very low cache hit rate (< 50%), indicating that most requests are cache misses. This suggests the cache strategy may need review rather than a sizing issue. Low hit rates can indicate improper cache key design, inappropriate TTL settings, or caching data that changes too frequently.',
      serviceName: 'AmazonElastiCache',
      recommendations: [
        'Review cache key design to ensure proper data segmentation and avoid key collisions.',
        'Evaluate TTL (Time To Live) settings - too short TTLs cause premature evictions, too long TTLs cache stale data.',
        'Analyze access patterns to determine if the cached data is appropriate for caching.',
        'Consider implementing cache warming strategies for frequently accessed data.',
        'Review application code to ensure cache-aside pattern is implemented correctly.',
      ],
      mitigations: [
        `**Review and optimize cache key design:**
\`\`\`python
# Bad: Generic keys that may collide
key = f"user_{user_id}"

# Good: Namespaced keys with version
key = f"user:profile:{user_id}:v1"
\`\`\`

**Adjust TTL settings in Redis:**
\`\`\`bash
redis-cli -h {{recommendation.cluster_endpoint}} SET mykey myvalue EX 3600  # 1 hour TTL
\`\`\`

**Implement cache warming:**
\`\`\`python
# Pre-populate cache with frequently accessed data
for popular_item in get_popular_items():
    cache.set(f"item:{popular_item.id}", popular_item.data, ttl=3600)
\`\`\`

**Monitor cache metrics:**
\`\`\`bash
aws cloudwatch get-metric-statistics \\
  --namespace AWS/ElastiCache \\
  --metric-name CacheHitRate \\
  --dimensions Name=CacheClusterId,Value={{recommendation.cluster_id}} \\
  --start-time 2024-01-01T00:00:00Z \\
  --end-time 2024-01-08T00:00:00Z \\
  --period 3600 \\
  --statistics Average
\`\`\`
`,
      ],
      references: [
        'https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/CacheMetrics.WhichShouldIMonitor.html',
        'https://aws.amazon.com/blogs/database/best-practices-for-amazon-elasticache-for-redis/',
      ],
    },
    // --- AWS DynamoDB Optimization ---
    aws_dynamodb_capacity_mode: {
      title: 'DynamoDB Table Should Use Optimal Capacity Mode',
      description:
        'DynamoDB tables can use either provisioned or on-demand capacity mode. Provisioned mode requires you to specify read and write capacity units, while on-demand mode automatically scales based on actual usage. Choosing the wrong capacity mode can lead to unnecessary costs or performance issues.',
      serviceName: 'AmazonDynamoDB',
      recommendations: [
        'For tables with low and unpredictable utilization (< 18%), switch from provisioned to on-demand mode to save costs while maintaining performance.',
        'For tables with steady and predictable usage (coefficient of variation < 30%), switch from on-demand to provisioned mode with appropriate capacity units to reduce costs.',
        'Monitor table utilization over a 7-day period to determine the optimal capacity mode for your workload.',
      ],
      mitigations: [
        `**Switch from Provisioned to On-Demand:**
\`\`\`bash
aws dynamodb update-table --table-name {{recommendation.table_name}} --billing-mode PAY_PER_REQUEST
\`\`\`

**Switch from On-Demand to Provisioned:**
\`\`\`bash
aws dynamodb update-table --table-name {{recommendation.table_name}} \\
  --billing-mode PROVISIONED \\
  --provisioned-throughput ReadCapacityUnits={{recommendation.recommended_read_units}},WriteCapacityUnits={{recommendation.recommended_write_units}}
\`\`\`

**Terraform configuration:**
\`\`\`hcl
resource "aws_dynamodb_table" "example" {
  name           = "{{recommendation.table_name}}"
  billing_mode   = "PAY_PER_REQUEST"  # or "PROVISIONED"
  # For provisioned mode:
  # read_capacity  = {{recommendation.recommended_read_units}}
  # write_capacity = {{recommendation.recommended_write_units}}
}
\`\`\`
`,
      ],
      references: [
        'https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/HowItWorks.ReadWriteCapacityMode.html',
        'https://aws.amazon.com/dynamodb/pricing/',
      ],
    },
    aws_dynamodb_autoscaling_disabled: {
      title: 'DynamoDB Provisioned Tables Should Have Auto-Scaling Enabled',
      description:
        'Auto-scaling for DynamoDB automatically adjusts read and write capacity based on actual traffic patterns. Without auto-scaling, provisioned tables may experience throttling during traffic spikes or waste money on unused capacity during low-traffic periods.',
      serviceName: 'AmazonDynamoDB',
      recommendations: [
        'Enable auto-scaling for both read and write capacity on provisioned DynamoDB tables to automatically adjust capacity based on utilization.',
        'Configure target utilization percentage (typically 70%) to balance cost and performance.',
        'Set appropriate minimum and maximum capacity limits to control costs while ensuring adequate performance.',
      ],
      mitigations: [
        `**Enable Auto-Scaling for Read Capacity:**
\`\`\`bash
aws application-autoscaling register-scalable-target \\
  --service-namespace dynamodb \\
  --resource-id table/{{recommendation.table_name}} \\
  --scalable-dimension dynamodb:table:ReadCapacityUnits \\
  --min-capacity 5 \\
  --max-capacity 100

aws application-autoscaling put-scaling-policy \\
  --service-namespace dynamodb \\
  --resource-id table/{{recommendation.table_name}} \\
  --scalable-dimension dynamodb:table:ReadCapacityUnits \\
  --policy-name {{recommendation.table_name}}-read-scaling-policy \\
  --policy-type TargetTrackingScaling \\
  --target-tracking-scaling-policy-configuration '{"TargetValue":70.0,"PredefinedMetricSpecification":{"PredefinedMetricType":"DynamoDBReadCapacityUtilization"}}'
\`\`\`

**Terraform configuration:**
\`\`\`hcl
resource "aws_appautoscaling_target" "dynamodb_table_read_target" {
  max_capacity       = 100
  min_capacity       = 5
  resource_id        = "table/{{recommendation.table_name}}"
  scalable_dimension = "dynamodb:table:ReadCapacityUnits"
  service_namespace  = "dynamodb"
}

resource "aws_appautoscaling_policy" "dynamodb_table_read_policy" {
  name               = "DynamoDBReadCapacityUtilization:table/{{recommendation.table_name}}"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.dynamodb_table_read_target.resource_id
  scalable_dimension = aws_appautoscaling_target.dynamodb_table_read_target.scalable_dimension
  service_namespace  = aws_appautoscaling_target.dynamodb_table_read_target.service_namespace

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "DynamoDBReadCapacityUtilization"
    }
    target_value = 70.0
  }
}
\`\`\`
`,
      ],
      references: [
        'https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/AutoScaling.html',
        'https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/AutoScaling.Console.html',
      ],
    },
    aws_dynamodb_ttl_disabled: {
      title: 'DynamoDB Tables Should Have Time To Live (TTL) Enabled',
      description:
        'Time To Live (TTL) for DynamoDB lets you define a per-item timestamp to determine when an item is no longer needed. After the TTL attribute value expires, DynamoDB deletes the item from your table automatically without consuming write throughput, helping you reduce storage costs by removing obsolete data.',
      serviceName: 'AmazonDynamoDB',
      recommendations: [
        'Enable TTL on DynamoDB tables that contain time-sensitive data to automatically delete expired items.',
        'Define a TTL attribute in your items that contains a timestamp indicating when the item should expire.',
        'TTL deletes items approximately 48 hours after expiration, so plan accordingly for time-sensitive data cleanup needs.',
      ],
      mitigations: [
        `**Enable TTL:**
\`\`\`bash
aws dynamodb update-time-to-live \\
  --table-name {{recommendation.table_name}} \\
  --time-to-live-specification "Enabled=true,AttributeName=ttl"
\`\`\`

**Terraform configuration:**
\`\`\`hcl
resource "aws_dynamodb_table" "example" {
  name           = "{{recommendation.table_name}}"
  billing_mode   = "PAY_PER_REQUEST"
  hash_key       = "id"

  attribute {
    name = "id"
    type = "S"
  }

  ttl {
    enabled        = true
    attribute_name = "ttl"
  }
}
\`\`\`

**Add TTL attribute to items (JavaScript example):**
\`\`\`javascript
// Set TTL to expire in 30 days
const ttlValue = Math.floor(Date.now() / 1000) + (30 * 24 * 60 * 60);

const params = {
  TableName: "{{recommendation.table_name}}",
  Item: {
    id: "example-id",
    ttl: ttlValue,
    // ... other attributes
  }
};
\`\`\`
`,
      ],
      references: [
        'https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/TTL.html',
        'https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/time-to-live-ttl-how-to.html',
      ],
    },
    // --- AWS RDS Aurora Serverless ---
    aws_rds_aurora_serverless_migration: {
      title: 'Aurora Provisioned Cluster Should Migrate to Serverless v2',
      description:
        'This Aurora provisioned cluster shows low utilization patterns (CPU < 20% and connections < 10 over 7 days). Aurora Serverless v2 can automatically scale down to 0.5 ACU (Aurora Capacity Units) during idle periods, providing significant cost savings for workloads with variable or unpredictable traffic.',
      serviceName: 'AmazonRDS',
      recommendations: [
        'Migrate to Aurora Serverless v2 for workloads with variable traffic patterns to reduce costs during idle periods.',
        'Aurora Serverless v2 scales instantly without connection drops, making it suitable for production workloads.',
        'Set appropriate minimum and maximum ACU limits based on your workload requirements.',
        'Monitor ACU usage after migration to optimize scaling configuration.',
      ],
      mitigations: [
        `**Create Aurora Serverless v2 Cluster:**
\`\`\`bash
aws rds create-db-cluster \\
  --db-cluster-identifier {{recommendation.new_cluster_id}} \\
  --engine {{recommendation.engine}} \\
  --engine-version {{recommendation.engine_version}} \\
  --master-username {{username}} \\
  --master-user-password {{password}} \\
  --serverless-v2-scaling-configuration MinCapacity=0.5,MaxCapacity=16

aws rds create-db-instance \\
  --db-instance-identifier {{recommendation.new_instance_id}} \\
  --db-cluster-identifier {{recommendation.new_cluster_id}} \\
  --db-instance-class db.serverless \\
  --engine {{recommendation.engine}}
\`\`\`

**Terraform configuration:**
\`\`\`hcl
resource "aws_rds_cluster" "example" {
  cluster_identifier      = "{{recommendation.cluster_id}}"
  engine                  = "{{recommendation.engine}}"
  engine_version          = "{{recommendation.engine_version}}"
  database_name           = "mydb"
  master_username         = "admin"
  master_password         = "password"

  serverlessv2_scaling_configuration {
    max_capacity = 16.0
    min_capacity = 0.5
  }
}

resource "aws_rds_cluster_instance" "example" {
  cluster_identifier = aws_rds_cluster.example.id
  instance_class     = "db.serverless"
  engine             = aws_rds_cluster.example.engine
  engine_version     = aws_rds_cluster.example.engine_version
}
\`\`\`

**Migration Steps:**
1. Create a snapshot of your existing cluster
2. Restore the snapshot to a new Aurora Serverless v2 cluster
3. Test the new cluster thoroughly
4. Update application connection strings
5. Delete the old provisioned cluster after validation
`,
      ],
      references: [
        'https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/aurora-serverless-v2.html',
        'https://aws.amazon.com/rds/aurora/pricing/',
      ],
    },
    aws_rds_aurora_serverless_scaling_config: {
      title: 'Aurora Serverless v2 Minimum Capacity Is Too High',
      description:
        'This Aurora Serverless v2 cluster has a minimum capacity setting that is significantly higher than the actual average ACU usage. Lowering the minimum capacity can reduce costs during low-traffic periods while still maintaining adequate performance.',
      serviceName: 'AmazonRDS',
      recommendations: [
        'Adjust minimum ACU capacity to match actual usage patterns, typically setting it close to average observed usage.',
        'Monitor ServerlessDatabaseCapacity metric over 7-14 days to understand usage patterns.',
        'Ensure minimum capacity is set high enough to handle baseline load without frequent scaling events.',
        'Consider keeping a small buffer (10-20%) above average usage for unexpected traffic spikes.',
      ],
      mitigations: [
        `**Update Scaling Configuration:**
\`\`\`bash
aws rds modify-db-cluster \\
  --db-cluster-identifier {{recommendation.db_instance_id}} \\
  --serverless-v2-scaling-configuration MinCapacity={{recommendation.recommended_min_capacity}},MaxCapacity={{recommendation.current_max_capacity}}
\`\`\`

**Terraform configuration:**
\`\`\`hcl
resource "aws_rds_cluster" "example" {
  cluster_identifier = "{{recommendation.db_instance_id}}"
  engine             = "{{recommendation.engine}}"
  engine_version     = "{{recommendation.engine_version}}"

  serverlessv2_scaling_configuration {
    max_capacity = {{recommendation.current_max_capacity}}
    min_capacity = {{recommendation.recommended_min_capacity}}
  }
}
\`\`\`

**Monitor ACU usage:**
\`\`\`bash
aws cloudwatch get-metric-statistics \\
  --namespace AWS/RDS \\
  --metric-name ServerlessDatabaseCapacity \\
  --dimensions Name=DBClusterIdentifier,Value={{recommendation.db_instance_id}} \\
  --start-time 2024-01-01T00:00:00Z \\
  --end-time 2024-01-08T00:00:00Z \\
  --period 3600 \\
  --statistics Average,Maximum,Minimum
\`\`\`
`,
      ],
      references: [
        'https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/aurora-serverless-v2.setting-capacity.html',
        'https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/aurora-serverless-v2.html',
      ],
    },
    // --- AWS Native Cost Explorer ---
    aws_native_ce_ri_recommendation: {
      title: 'AWS Reserved Instance Recommendation',
      description:
        'AWS Cost Explorer has identified potential savings by purchasing Reserved Instances for resources with steady-state usage. Reserved Instances can provide up to 72% discount compared to On-Demand pricing.',
      serviceName: 'AWSCostExplorer',
      recommendations: ['Review and consider purchasing Reserved Instances for workloads with predictable, steady-state usage patterns.'],
      mitigations: [
        'Evaluate the RI recommendation in AWS Cost Explorer and purchase appropriate Reserved Instances based on your usage patterns and commitment preferences (1-year or 3-year, All Upfront, Partial, or No Upfront).',
      ],
      references: ['https://docs.aws.amazon.com/cost-management/latest/userguide/ri-recommendations.html'],
    },
    aws_native_ce_savings_plan_recommendation: {
      title: 'AWS Savings Plan Recommendation',
      description:
        'AWS Cost Explorer has identified potential savings through Savings Plans. Savings Plans offer flexible pricing in exchange for a commitment to a consistent amount of usage (measured in $/hour) for a 1- or 3-year term.',
      serviceName: 'AWSCostExplorer',
      recommendations: ['Review and consider purchasing Savings Plans for workloads with consistent compute usage.'],
      mitigations: [
        'Evaluate the Savings Plan recommendation in AWS Cost Explorer and purchase appropriate plans based on your compute usage patterns.',
      ],
      references: ['https://docs.aws.amazon.com/cost-management/latest/userguide/sp-recommendations.html'],
    },
    // --- AWS Native Compute Optimizer ---
    aws_native_co_ec2_rightsize: {
      title: 'AWS Compute Optimizer EC2 Right-Sizing',
      description:
        'AWS Compute Optimizer has analyzed EC2 instance utilization and identified that this instance is over-provisioned or under-provisioned. Rightsizing to the recommended instance type can improve performance or reduce cost.',
      serviceName: 'AmazonEC2',
      recommendations: ['Follow the AWS Compute Optimizer recommendation to resize the EC2 instance to the suggested instance type.'],
      mitigations: [
        `Stop the instance, change its type, and restart:
\`\`\`
aws ec2 stop-instances --instance-ids {{recommendation.instance_id}}
aws ec2 modify-instance-attribute --instance-id {{recommendation.instance_id}} --instance-type '{"Value":"{{recommendation.recommended_instance_type}}"}'
aws ec2 start-instances --instance-ids {{recommendation.instance_id}}
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/compute-optimizer/latest/ug/viewing-recommendations.html'],
    },
    aws_native_co_lambda_rightsize: {
      title: 'AWS Compute Optimizer Lambda Right-Sizing',
      description:
        'AWS Compute Optimizer has analyzed Lambda function invocations and identified that the configured memory is not optimal. Adjusting memory allocation can improve performance and/or reduce cost.',
      serviceName: 'AWSLambda',
      recommendations: ['Adjust Lambda function memory configuration based on AWS Compute Optimizer recommendations.'],
      mitigations: [
        `Update function memory:
\`\`\`
aws lambda update-function-configuration --function-name {{resource_name}} --memory-size 512
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/compute-optimizer/latest/ug/viewing-recommendations.html'],
    },
    aws_native_co_ebs_rightsize: {
      title: 'AWS Compute Optimizer EBS Right-Sizing',
      description:
        'AWS Compute Optimizer has analyzed EBS volume performance and identified optimization opportunities. The volume may be over-provisioned in terms of IOPS or throughput.',
      serviceName: 'AmazonEC2',
      recommendations: ['Follow Compute Optimizer recommendations to modify EBS volume type, size, or IOPS configuration.'],
      mitigations: [
        `Modify the EBS volume:
\`\`\`
aws ec2 modify-volume --volume-id vol-01234abcd --volume-type gp3 --iops 3000 --throughput 125
\`\`\`
`,
      ],
      references: ['https://docs.aws.amazon.com/compute-optimizer/latest/ug/viewing-recommendations.html'],
    },
    aws_native_co_ecs_rightsize: {
      title: 'AWS Compute Optimizer ECS Right-Sizing',
      description:
        'AWS Compute Optimizer has analyzed ECS task resource utilization and identified that CPU or memory allocations are not optimal for the workload.',
      serviceName: 'AmazonECS',
      recommendations: ['Adjust ECS task definition CPU and memory based on Compute Optimizer recommendations.'],
      mitigations: ['Register a new task definition revision with the recommended CPU and memory values, then update the ECS service.'],
      references: ['https://docs.aws.amazon.com/compute-optimizer/latest/ug/viewing-recommendations.html'],
    },
    // --- AWS Native Cost Optimization Hub ---
    aws_native_purchase_reserved_instances: {
      title: 'AWS Purchase Reserved Instances',
      description:
        'AWS Cost Optimization Hub has identified potential savings by purchasing Reserved Instances. Reserved Instances provide a significant discount compared to On-Demand pricing in exchange for a usage commitment.',
      serviceName: 'AWSCostOptimizationHub',
      recommendations: ['Review the recommended Reserved Instance purchase and evaluate whether your usage patterns justify the commitment.'],
      mitigations: [
        'Navigate to the AWS Cost Optimization Hub console to review the full recommendation details and purchase Reserved Instances through the AWS Management Console.',
      ],
      references: ['https://docs.aws.amazon.com/cost-management/latest/userguide/ce-ri-recommendations.html'],
    },
    aws_native_purchase_savings_plans: {
      title: 'AWS Purchase Savings Plans',
      description:
        'AWS Cost Optimization Hub has identified potential savings through Savings Plans. Savings Plans offer flexible pricing in exchange for a commitment to a consistent amount of usage for a 1- or 3-year term.',
      serviceName: 'AWSCostOptimizationHub',
      recommendations: ['Review the recommended Savings Plan purchase and evaluate whether your compute usage patterns justify the commitment.'],
      mitigations: [
        'Navigate to the AWS Cost Optimization Hub console to review the full recommendation details and purchase Savings Plans through the AWS Management Console.',
      ],
      references: ['https://docs.aws.amazon.com/cost-management/latest/userguide/sp-recommendations.html'],
    },
    aws_native_rightsize: {
      title: 'AWS Right-Size Resource',
      description:
        'AWS Cost Optimization Hub has identified a resource that is over-provisioned relative to its usage. Right-sizing to a smaller or more appropriate configuration can reduce costs.',
      serviceName: 'AWSCostOptimizationHub',
      recommendations: ['Review the recommended resource configuration and right-size the resource to match actual usage.'],
      mitigations: [
        'Navigate to the AWS Cost Optimization Hub console to review the full recommendation details and apply the suggested configuration change.',
      ],
      references: ['https://docs.aws.amazon.com/cost-management/latest/userguide/cost-optimization-hub.html'],
    },
    aws_native_stop: {
      title: 'AWS Stop Idle Resource',
      description: 'AWS Cost Optimization Hub has identified an idle or underutilized resource that can be stopped to reduce costs.',
      serviceName: 'AWSCostOptimizationHub',
      recommendations: ['Review the resource utilization and stop or terminate the idle resource if it is no longer needed.'],
      mitigations: ['Navigate to the AWS Cost Optimization Hub console to review the full recommendation details and stop the resource.'],
      references: ['https://docs.aws.amazon.com/cost-management/latest/userguide/cost-optimization-hub.html'],
    },
    aws_native_delete: {
      title: 'AWS Delete Unused Resource',
      description: 'AWS Cost Optimization Hub has identified an unused resource that can be deleted to eliminate unnecessary costs.',
      serviceName: 'AWSCostOptimizationHub',
      recommendations: ['Review the resource and delete it if it is confirmed to be unused and no longer needed.'],
      mitigations: ['Navigate to the AWS Cost Optimization Hub console to review the full recommendation details and delete the resource.'],
      references: ['https://docs.aws.amazon.com/cost-management/latest/userguide/cost-optimization-hub.html'],
    },
    aws_native_upgrade: {
      title: 'AWS Upgrade Resource',
      description:
        'AWS Cost Optimization Hub has identified a resource running an older generation that can be upgraded to a newer, more cost-effective generation.',
      serviceName: 'AWSCostOptimizationHub',
      recommendations: ['Review the recommended upgrade and migrate the resource to the newer generation for better price-performance.'],
      mitigations: ['Navigate to the AWS Cost Optimization Hub console to review the full recommendation details and apply the upgrade.'],
      references: ['https://docs.aws.amazon.com/cost-management/latest/userguide/cost-optimization-hub.html'],
    },
    aws_native_migrate_graviton: {
      title: 'AWS Migrate to Graviton',
      description:
        'AWS Cost Optimization Hub has identified a resource that can be migrated to AWS Graviton processors for better price-performance. Graviton-based instances deliver up to 40% better price-performance.',
      serviceName: 'AWSCostOptimizationHub',
      recommendations: [
        'Review application compatibility and migrate the resource to a Graviton-based instance type for improved price-performance.',
      ],
      mitigations: ['Navigate to the AWS Cost Optimization Hub console to review the full recommendation details and plan the Graviton migration.'],
      references: ['https://docs.aws.amazon.com/cost-management/latest/userguide/cost-optimization-hub.html'],
    },
    // --- GCP RightSizing ---
    gcp_gke_inactive_cluster: {
      title: 'GKE Cluster is Inactive',
      description: 'This GKE cluster has very low or no workload activity. Idle clusters still incur costs for the control plane and node pools.',
      serviceName: 'GKE',
      recommendations: ['Delete inactive GKE clusters or scale down node pools to zero if the cluster may be needed later.'],
      mitigations: [
        `Scale node pool to zero or delete cluster:
\`\`\`
gcloud container clusters resize {{resource_name}} --node-pool=default-pool --num-nodes=0 --zone=us-central1-a
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/kubernetes-engine/docs/how-to/deleting-a-cluster'],
    },
    gcp_function_not_active: {
      title: 'Cloud Function Not Active',
      description:
        'This Cloud Function has had zero or very few invocations over the monitoring period. While Cloud Functions have no idle cost, associated resources (VPC connectors, allocated memory) may still incur charges.',
      serviceName: 'CloudFunctions',
      recommendations: ['Review and delete Cloud Functions that are no longer needed.'],
      mitigations: [
        `Delete the function:
\`\`\`
gcloud functions delete {{resource_name}} --region=us-central1
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/functions/docs/monitoring'],
    },
    gcp_function_high_memory: {
      title: 'Cloud Function Over-Provisioned Memory',
      description:
        'This Cloud Function is allocated more memory than it uses. Over-provisioned memory increases cost per invocation without benefit.',
      serviceName: 'CloudFunctions',
      recommendations: ['Reduce Cloud Function memory allocation to match actual usage.'],
      mitigations: [
        `Update function memory:
\`\`\`
gcloud functions deploy {{resource_name}} --memory=256MB --region=us-central1
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/functions/docs/configuring/memory'],
    },
    gcp_bigquery_table_unused: {
      title: 'BigQuery Table Unused',
      description: 'This BigQuery table has not been queried or modified for an extended period. Unused tables still incur storage costs.',
      serviceName: 'BigQuery',
      recommendations: [
        'Delete or archive unused BigQuery tables to reduce storage costs. Consider exporting to Cloud Storage if data retention is required.',
      ],
      mitigations: [
        `Delete the table or set expiration:
\`\`\`
bq update --expiration 0 my_dataset.my_table
bq rm my_dataset.my_table
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/bigquery/docs/managing-tables'],
    },
    gcp_run_high_memory: {
      title: 'Cloud Run Service Over-Provisioned Memory',
      description: 'This Cloud Run service is allocated more memory than it uses. Reducing memory allocation can lower costs.',
      serviceName: 'CloudRun',
      recommendations: ['Reduce Cloud Run service memory limit to match actual usage patterns.'],
      mitigations: [
        `Update service memory:
\`\`\`
gcloud run services update {{resource_name}} --memory=512Mi --region=us-central1
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/run/docs/configuring/memory-limits'],
    },
    gcp_compute_stopped_instance: {
      title: 'GCE Stopped Instance Incurring Costs',
      description:
        'Stopped Compute Engine instances still incur charges for attached persistent disks, static external IP addresses, and other associated resources.',
      serviceName: 'ComputeEngine',
      recommendations: ['Delete stopped instances that are no longer needed, or snapshot and delete their disks to reduce costs.'],
      mitigations: [
        `Delete the stopped instance:
\`\`\`
gcloud compute instances delete {{resource_name}} --zone=us-central1-a
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/compute/docs/instances/stop-start-instance'],
    },
    gcp_sql_inactive_instance: {
      title: 'Cloud SQL Instance Inactive',
      description:
        'This Cloud SQL instance has very few or no connections over the monitoring period. Idle Cloud SQL instances still incur full instance costs.',
      serviceName: 'CloudSQL',
      recommendations: ['Stop or delete inactive Cloud SQL instances. Consider using Cloud SQL start/stop feature for development instances.'],
      mitigations: [
        `Stop or delete the instance:
\`\`\`
gcloud sql instances patch {{resource_name}} --activation-policy=NEVER
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/sql/docs/mysql/start-stop-restart-instance'],
    },
    gcp_lb_unused_forwarding_rule: {
      title: 'Unused Load Balancer Forwarding Rule',
      description:
        'This load balancer forwarding rule has no backends or receives negligible traffic. Unused forwarding rules still incur hourly charges.',
      serviceName: 'CloudLoadBalancing',
      recommendations: ['Delete unused load balancer forwarding rules and associated backend services.'],
      mitigations: [
        `Delete the forwarding rule:
\`\`\`
gcloud compute forwarding-rules delete {{resource_name}} --region=us-central1
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/load-balancing/docs/forwarding-rule-concepts'],
    },
    gcp_storage_class_optimization: {
      title: 'GCS Bucket Storage Class Optimization',
      description:
        'This GCS bucket contains objects that could be stored in a cheaper storage class based on access patterns. Moving infrequently accessed data to Nearline, Coldline, or Archive can significantly reduce costs.',
      serviceName: 'CloudStorage',
      recommendations: ['Configure lifecycle rules to automatically transition objects to cheaper storage classes based on access frequency.'],
      mitigations: [
        `Set lifecycle rule to transition objects:
\`\`\`
gsutil lifecycle set lifecycle.json gs://{{resource_name}}
\`\`\`
Where lifecycle.json transitions objects older than 30 days to Nearline.`,
      ],
      references: ['https://cloud.google.com/storage/docs/storage-classes'],
    },
    // --- GCP Native Recommender RightSizing ---
    gcp_native_compute_instance_idle_resource: {
      title: 'GCP Recommender: Idle Compute Instance',
      description:
        'Google Cloud Recommender has identified this Compute Engine instance as idle based on low CPU, network, and disk utilization. Idle instances waste resources and incur unnecessary costs.',
      serviceName: 'ComputeEngine',
      recommendations: ['Stop or delete the idle Compute Engine instance as recommended by GCP Recommender.'],
      mitigations: [
        `Stop or delete the instance:
\`\`\`
gcloud compute instances stop {{resource_name}} --zone=us-central1-a
gcloud compute instances delete {{resource_name}} --zone=us-central1-a
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/recommender/docs/recommenders'],
    },
    gcp_native_compute_instance_machine_type: {
      title: 'GCP Recommender: Right-Size Compute Instance',
      description:
        'Google Cloud Recommender has analyzed this Compute Engine instance and recommends changing to a different machine type for better cost-efficiency or performance.',
      serviceName: 'ComputeEngine',
      recommendations: ['Resize the Compute Engine instance to the recommended machine type.'],
      mitigations: [
        `Stop, resize, and restart:
\`\`\`
gcloud compute instances stop {{resource_name}} --zone=us-central1-a
gcloud compute instances set-machine-type {{resource_name}} --machine-type=e2-medium --zone=us-central1-a
gcloud compute instances start {{resource_name}} --zone=us-central1-a
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/recommender/docs/recommenders'],
    },
    gcp_native_compute_disk_idle_resource: {
      title: 'GCP Recommender: Idle Persistent Disk',
      description:
        'Google Cloud Recommender has identified this persistent disk as idle (not attached to any instance or has no I/O activity). Idle disks incur storage costs without providing value.',
      serviceName: 'ComputeEngine',
      recommendations: ['Snapshot and delete idle persistent disks.'],
      mitigations: [
        `Snapshot and delete the disk:
\`\`\`
gcloud compute disks snapshot {{resource_name}} --zone=us-central1-a --snapshot-names=my-disk-backup
gcloud compute disks delete {{resource_name}} --zone=us-central1-a
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/recommender/docs/recommenders'],
    },
    gcp_native_compute_address_idle_resource: {
      title: 'GCP Recommender: Idle Static IP Address',
      description:
        'Google Cloud Recommender has identified this static external IP address as idle (not associated with any resource). Idle static IPs incur hourly charges.',
      serviceName: 'ComputeEngine',
      recommendations: ['Release idle static IP addresses that are not in use.'],
      mitigations: [
        `Release the static IP:
\`\`\`
gcloud compute addresses delete {{resource_name}} --region=us-central1
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/recommender/docs/recommenders'],
    },
    gcp_native_compute_image_idle_resource: {
      title: 'GCP Recommender: Idle Compute Image',
      description:
        'Google Cloud Recommender has identified this custom image as idle (not used to create instances). Idle images incur storage costs.',
      serviceName: 'ComputeEngine',
      recommendations: ['Delete unused custom images to reduce storage costs.'],
      mitigations: [
        `Delete the image:
\`\`\`
gcloud compute images delete {{resource_name}}
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/recommender/docs/recommenders'],
    },
    gcp_native_cloudsql_instance_idle: {
      title: 'GCP Recommender: Idle Cloud SQL Instance',
      description:
        'Google Cloud Recommender has identified this Cloud SQL instance as idle based on very low connection and query activity. Idle instances incur full costs.',
      serviceName: 'CloudSQL',
      recommendations: ['Stop or delete idle Cloud SQL instances as recommended by GCP Recommender.'],
      mitigations: [
        `Stop the instance:
\`\`\`
gcloud sql instances patch {{resource_name}} --activation-policy=NEVER
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/recommender/docs/recommenders'],
    },
    gcp_native_cloudsql_instance_overprovisioned: {
      title: 'GCP Recommender: Overprovisioned Cloud SQL Instance',
      description:
        'Google Cloud Recommender has identified this Cloud SQL instance as overprovisioned. The allocated CPU and memory exceed the actual workload requirements, resulting in unnecessary costs.',
      serviceName: 'CloudSQL',
      recommendations: ['Resize the Cloud SQL instance to a smaller machine type as recommended by GCP Recommender.'],
      mitigations: [
        `Resize the instance (requires restart):
\`\`\`
gcloud sql instances patch {{resource_name}} --tier=db-custom-2-7680
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/recommender/docs/recommenders'],
    },
    gcp_native_container_diagnosis: {
      title: 'GCP Recommender: GKE Container Diagnosis',
      description:
        'Google Cloud Recommender has identified potential issues with GKE container resource configurations, such as over-provisioned or under-provisioned CPU/memory requests and limits.',
      serviceName: 'GKE',
      recommendations: ['Review and adjust container resource requests and limits based on GCP Recommender suggestions.'],
      mitigations: [
        'Update Kubernetes deployment manifests to adjust resource requests and limits according to the recommendation. Apply changes with kubectl apply.',
      ],
      references: ['https://cloud.google.com/recommender/docs/recommenders'],
    },
    gcp_native_cloudsql_instance_underprovisioned: {
      title: 'GCP Recommender: Underprovisioned Cloud SQL Instance',
      description:
        'Google Cloud Recommender has identified this Cloud SQL instance as underprovisioned. The instance may experience performance issues due to insufficient CPU or memory.',
      serviceName: 'CloudSQL',
      recommendations: ['Upgrade the Cloud SQL instance to a larger machine type as recommended by GCP Recommender.'],
      mitigations: [
        `Resize the instance (requires restart):
\`\`\`
gcloud sql instances patch {{resource_name}} --tier=db-custom-4-15360
\`\`\`
`,
      ],
      references: ['https://cloud.google.com/recommender/docs/recommenders'],
    },
    // --- Azure RightSizing ---
    azure_unassociated_public_ip: {
      title: 'Unassociated Azure Public IP Address',
      description:
        'Azure Public IP addresses that are not associated with any resource still incur hourly charges. Unassociated IPs should be deleted if no longer needed.',
      serviceName: 'AzureNetworking',
      recommendations: ['Delete unassociated public IP addresses to reduce costs.'],
      mitigations: [
        `Delete the public IP:
\`\`\`
az network public-ip delete --name {{resource_name}} --resource-group {{resource_group}}
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/ip-services/public-ip-addresses'],
    },
    azure_unused_load_balancer: {
      title: 'Unused Azure Load Balancer',
      description: 'This Azure Load Balancer has no backend pool members or receives negligible traffic. Unused load balancers still incur charges.',
      serviceName: 'AzureLoadBalancer',
      recommendations: ['Delete unused Azure Load Balancers that have no backend pool members.'],
      mitigations: [
        `Delete the load balancer:
\`\`\`
az network lb delete --name {{resource_name}} --resource-group {{resource_group}}
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-overview'],
    },
    azure_nic_orphaned: {
      title: 'Orphaned Azure Network Interface',
      description:
        'This network interface is not attached to any virtual machine. Orphaned NICs may have associated public IPs or NSGs that incur costs.',
      serviceName: 'AzureNetworking',
      recommendations: ['Delete orphaned network interfaces and their associated resources.'],
      mitigations: [
        `Delete the NIC:
\`\`\`
az network nic delete --name {{resource_name}} --resource-group {{resource_group}}
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-network/virtual-network-network-interface'],
    },
    azure_disk_unattached_volume: {
      title: 'Unattached Azure Managed Disk',
      description: 'This managed disk is not attached to any virtual machine. Unattached disks incur storage costs without providing value.',
      serviceName: 'AzureManagedDisks',
      recommendations: ['Snapshot and delete unattached managed disks to reduce storage costs.'],
      mitigations: [
        `Create snapshot and delete disk:
\`\`\`
az snapshot create --name {{resource_name}}-snapshot --resource-group {{resource_group}} --source {{resource_name}}
az disk delete --name {{resource_name}} --resource-group {{resource_group}}
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/disks-find-unattached-portal'],
    },
    azure_storage_account_potentially_idle: {
      title: 'Potentially Idle Azure Storage Account',
      description:
        'This storage account has very low transaction activity over the monitoring period. It may be unused and incurring unnecessary costs.',
      serviceName: 'AzureStorage',
      recommendations: ['Evaluate whether this storage account is still needed. Delete or consolidate if unused.'],
      mitigations: ['Review storage account usage in Azure Monitor metrics. If confirmed idle, migrate any remaining data and delete the account.'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-account-overview'],
    },
    azure_appgateway_stopped: {
      title: 'Stopped Azure Application Gateway',
      description:
        'This Application Gateway is in a stopped state. While stopped gateways have reduced costs, they may still incur charges for the public IP and other associated resources.',
      serviceName: 'AzureAppGateway',
      recommendations: ['Delete stopped Application Gateways that are no longer needed.'],
      mitigations: [
        `Delete the Application Gateway:
\`\`\`
az network application-gateway delete --name {{resource_name}} --resource-group {{resource_group}}
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/application-gateway/overview'],
    },
    azure_app_service_stopped_app: {
      title: 'Stopped Azure App Service',
      description:
        'This App Service is in a stopped state but still occupies resources in its App Service Plan. If the plan is not shared with other apps, it continues to incur full charges.',
      serviceName: 'AzureAppService',
      recommendations: ['Delete stopped App Services that are no longer needed, or consolidate them into shared plans.'],
      mitigations: [
        `Delete the app:
\`\`\`
az webapp delete --name {{resource_name}} --resource-group {{resource_group}}
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/app-service/overview'],
    },
    azure_redis_overprovisioned_sku: {
      title: 'Azure Redis Cache Overprovisioned SKU',
      description:
        'This Azure Cache for Redis instance is using a higher SKU tier or cache size than its workload requires. Downgrading can reduce costs.',
      serviceName: 'AzureRedis',
      recommendations: ['Evaluate Redis cache utilization and consider downgrading to a smaller SKU.'],
      mitigations: [
        'Note: Downgrading Redis SKU requires creating a new instance with the desired SKU and migrating data. Azure does not support in-place SKU downgrades.',
      ],
      references: ['https://learn.microsoft.com/en-us/azure/azure-cache-for-redis/cache-overview'],
    },
    azure_sql_database_pricing_model_upgrade: {
      title: 'Azure SQL Database Pricing Model Optimization',
      description:
        'This Azure SQL Database may benefit from switching to a different pricing model (e.g., from DTU to vCore, or vice versa) based on its workload characteristics.',
      serviceName: 'AzureSQL',
      recommendations: ['Evaluate whether switching the Azure SQL pricing model would reduce costs for the current workload.'],
      mitigations: ['Review Azure SQL Database pricing models and use the Azure pricing calculator to compare DTU vs vCore costs for your workload.'],
      references: ['https://learn.microsoft.com/en-us/azure/azure-sql/database/service-tiers-general-purpose-business-critical'],
    },
    azure_sql_serverless_optimization: {
      title: 'Azure SQL Serverless Optimization',
      description:
        'This Azure SQL Database has intermittent usage patterns that could benefit from the serverless compute tier, which auto-pauses during inactivity and scales automatically.',
      serviceName: 'AzureSQL',
      recommendations: ['Consider switching to Azure SQL Serverless for databases with intermittent, unpredictable usage patterns.'],
      mitigations: [
        `Convert to serverless:
\`\`\`
az sql db update --name {{resource_name}} --resource-group {{resource_group}} --server {{recommendation.server_name}} --edition GeneralPurpose --compute-model Serverless --auto-pause-delay 60
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/azure-sql/database/serverless-tier-overview'],
    },
    azure_storage_redundancy_optimization: {
      title: 'Azure Storage Redundancy Optimization',
      description:
        'This storage account may be using a higher redundancy level (e.g., GRS, RA-GRS) than required. Switching to a lower redundancy level (e.g., LRS) can reduce costs.',
      serviceName: 'AzureStorage',
      recommendations: ['Evaluate whether a lower storage redundancy level is acceptable for this workload.'],
      mitigations: [
        `Change storage redundancy:
\`\`\`
az storage account update --name {{resource_name}} --resource-group {{resource_group}} --sku Standard_LRS
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-redundancy'],
    },
    azure_storage_performance_tier_upgrade: {
      title: 'Azure Storage Performance Tier Optimization',
      description:
        'This storage account is using a performance tier that may not match its access patterns. Standard tier may be sufficient for infrequently accessed data.',
      serviceName: 'AzureStorage',
      recommendations: ['Evaluate whether the storage account performance tier matches the actual access patterns.'],
      mitigations: [
        'Review storage account metrics and consider switching between Premium and Standard tiers based on IOPS and latency requirements.',
      ],
      references: ['https://learn.microsoft.com/en-us/azure/storage/common/storage-account-overview'],
    },
    azure_storage_access_tier_optimization: {
      title: 'Azure Storage Access Tier Optimization',
      description:
        'This storage account contains blobs in the Hot access tier that are infrequently accessed. Moving them to Cool or Archive tier can significantly reduce storage costs.',
      serviceName: 'AzureStorage',
      recommendations: ['Configure lifecycle management policies to automatically move infrequently accessed blobs to cooler tiers.'],
      mitigations: [
        `Set lifecycle management policy:
\`\`\`
az storage account management-policy create --account-name {{resource_name}} --resource-group {{resource_group}} --policy @policy.json
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/storage/blobs/access-tiers-overview'],
    },
    azure_files_unused_file_share: {
      title: 'Unused Azure File Share',
      description: 'This Azure File Share has very low transaction activity and may be unused. Idle file shares still incur storage costs.',
      serviceName: 'AzureStorage',
      recommendations: ['Delete unused Azure File Shares to reduce storage costs.'],
      mitigations: [
        `Delete the file share:
\`\`\`
az storage share delete --name {{resource_name}} --account-name {{resource_name}}
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/storage/files/storage-files-introduction'],
    },
    azure_vmss_empty: {
      title: 'Empty Azure VM Scale Set',
      description:
        'This VM Scale Set has zero instances but the scale set resource itself still exists. While there are no VM costs, the associated resources (load balancer, public IP) may still incur charges.',
      serviceName: 'AzureVMSS',
      recommendations: ['Delete empty VM Scale Sets that are no longer needed.'],
      mitigations: [
        `Delete the scale set:
\`\`\`
az vmss delete --name {{resource_name}} --resource-group {{resource_group}}
\`\`\`
`,
      ],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/overview'],
    },
    // --- Azure Native Advisor RightSizing ---
    azure_native_advisor_cost: {
      title: 'Azure Advisor: Cost Recommendation',
      description:
        'Azure Advisor has identified an opportunity to reduce costs. This may include right-sizing VMs, purchasing reservations, deleting unused resources, or other cost optimizations.',
      serviceName: 'AzureAdvisor',
      recommendations: ['Review and implement the Azure Advisor cost recommendation to reduce your Azure spending.'],
      mitigations: [
        'Follow the specific remediation steps provided in the Azure Advisor recommendation details. Actions vary by recommendation type.',
      ],
      references: ['https://learn.microsoft.com/en-us/azure/advisor/advisor-cost-recommendations'],
    },
    azure_native_advisor_highavailability: {
      title: 'Azure Advisor: High Availability Recommendation',
      description:
        'Azure Advisor has identified a potential improvement for the availability of your Azure resources. This may include enabling zone redundancy, configuring backup, or other HA improvements.',
      serviceName: 'AzureAdvisor',
      recommendations: ['Review and implement the Azure Advisor high availability recommendation to improve resilience.'],
      mitigations: ['Follow the specific remediation steps provided in the Azure Advisor recommendation details.'],
      references: ['https://learn.microsoft.com/en-us/azure/advisor/advisor-high-availability-recommendations'],
    },
    azure_native_advisor_operationalexcellence: {
      title: 'Azure Advisor: Operational Excellence Recommendation',
      description:
        'Azure Advisor has identified an opportunity to improve operational practices. This may include enabling diagnostics, updating configurations, or other operational improvements.',
      serviceName: 'AzureAdvisor',
      recommendations: ['Review and implement the Azure Advisor operational excellence recommendation.'],
      mitigations: ['Follow the specific remediation steps provided in the Azure Advisor recommendation details.'],
      references: ['https://learn.microsoft.com/en-us/azure/advisor/advisor-operational-excellence-recommendations'],
    },
    azure_native_advisor_performance: {
      title: 'Azure Advisor: Performance Recommendation',
      description:
        'Azure Advisor has identified an opportunity to improve performance. This may include right-sizing resources, enabling caching, optimizing queries, or other performance improvements.',
      serviceName: 'AzureAdvisor',
      recommendations: ['Review and implement the Azure Advisor performance recommendation.'],
      mitigations: ['Follow the specific remediation steps provided in the Azure Advisor recommendation details.'],
      references: ['https://learn.microsoft.com/en-us/azure/advisor/advisor-performance-recommendations'],
    },
    azure_app_service_plan_optimization: {
      title: 'Azure App Service Plan Should Be Optimized',
      description:
        'The App Service Plan may be over-provisioned for the current workload. Consider scaling down or using a more cost-effective tier.',
      serviceName: 'AppService',
      recommendations: [
        'The App Service Plan may be over-provisioned for the current workload. Consider scaling down or using a more cost-effective tier.',
      ],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/app-service/overview-hosting-plans'],
    },
    azure_files_large_quota: {
      title: 'Azure Files Share Has Very Large Quota',
      description: 'The file share has an unusually large quota that may be over-provisioned.',
      serviceName: 'AzureFiles',
      recommendations: ['The file share has an unusually large quota that may be over-provisioned.'],
      mitigations: ['Review and remediate this finding. Refer to the documentation link below for detailed steps.'],
      compliances: ['APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/storage/files/storage-files-planning'],
    },
    azure_vm_idle_instance: {
      title: 'Azure VM Is Idle and Should Be Stopped or Deallocated',
      description:
        'This Azure Virtual Machine has been identified as idle based on low CPU utilization and minimal network activity over a sustained period. Idle VMs continue to incur compute charges even when not performing useful work.',
      serviceName: 'VirtualMachines',
      recommendations: [
        "Review the VM's workload and determine if it is still needed. If the VM is no longer required, deallocate or delete it. If the workload is intermittent, consider using Azure Auto-Shutdown or scaling down to a smaller VM size.",
      ],
      mitigations: [
        `Deallocate the idle VM to stop incurring compute charges:
\`\`\`
az vm deallocate \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}
\`\`\`

Or configure auto-shutdown to automatically deallocate during off-hours:
\`\`\`
az vm auto-shutdown \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --time 1900
\`\`\``,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/states-billing'],
    },
    azure_vm_premium_ssd_os_disk_used: {
      title: 'Azure VM Using Premium SSD OS Disk May Be Over-Provisioned',
      description:
        "This VM is using a Premium SSD for the OS disk. For workloads that don't require high IOPS or low latency for the OS disk, switching to Standard SSD can reduce costs while maintaining adequate performance.",
      serviceName: 'VirtualMachines',
      recommendations: [
        'Evaluate whether the OS disk requires Premium SSD performance. For most general-purpose workloads, Standard SSD provides sufficient performance at a lower cost.',
      ],
      mitigations: [
        `Convert the OS disk from Premium SSD to Standard SSD:
\`\`\`
az vm deallocate \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}

az disk update \\
  --resource-group {{resource_group}} \\
  --name {{os_disk_name}} \\
  --sku StandardSSD_LRS

az vm start \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}}
\`\`\``,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/disks-types'],
    },
    azure_vm_underutilized: {
      title: 'Azure VM Is Underutilized and Should Be Right-Sized',
      description:
        'This Azure Virtual Machine is consistently using a small fraction of its allocated CPU and memory resources. Right-sizing to a smaller VM SKU can significantly reduce costs while maintaining adequate performance for the workload.',
      serviceName: 'VirtualMachines',
      recommendations: [
        "Analyze the VM's resource utilization metrics and resize to a smaller VM SKU that matches the actual workload requirements. Consider the Bs-series for burstable workloads or a smaller size within the same family.",
      ],
      mitigations: [
        `Resize the VM to a smaller SKU:
\`\`\`
az vm resize \\
  --resource-group {{resource_group}} \\
  --name {{resource_name}} \\
  --size Standard_B2s
\`\`\`

Note: The VM will be restarted during the resize operation. Plan for a brief downtime window.`,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://learn.microsoft.com/en-us/azure/virtual-machines/resize-vm'],
    },
    gcp_compute_idle_instance: {
      title: 'GCP Compute Instance Is Idle and Should Be Stopped',
      description:
        'This GCP Compute Engine instance has been identified as idle based on consistently low CPU utilization and minimal network activity. Idle instances continue to incur compute and licensing charges.',
      serviceName: 'ComputeEngine',
      recommendations: [
        "Review the instance's workload and stop or delete it if no longer needed. Consider scheduling start/stop for intermittent workloads.",
      ],
      mitigations: [
        `Stop the idle instance:
\`\`\`
gcloud compute instances stop {{instance_name}} \\
  --zone={{zone}}
\`\`\`

Or set up an instance schedule for automatic start/stop:
\`\`\`
gcloud compute resource-policies create instance-schedule {{schedule_name}} \\
  --region={{region}} \\
  --vm-start-schedule='0 8 * * MON-FRI' \\
  --vm-stop-schedule='0 18 * * MON-FRI' \\
  --timezone=America/New_York
\`\`\``,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://cloud.google.com/compute/docs/instances/stop-start-instance'],
    },
    gcp_compute_underutilized: {
      title: 'GCP Compute Instance Is Underutilized and Should Be Right-Sized',
      description:
        'This GCP Compute Engine instance is consistently using a small fraction of its allocated CPU and memory resources. Right-sizing to a smaller machine type can reduce costs while maintaining adequate performance.',
      serviceName: 'ComputeEngine',
      recommendations: [
        "Analyze the instance's resource utilization and resize to a smaller machine type that matches actual workload requirements.",
      ],
      mitigations: [
        `Resize the instance to a smaller machine type:
\`\`\`
gcloud compute instances stop {{instance_name}} \\
  --zone={{zone}}

gcloud compute instances set-machine-type {{instance_name}} \\
  --zone={{zone}} \\
  --machine-type=e2-medium

gcloud compute instances start {{instance_name}} \\
  --zone={{zone}}
\`\`\``,
      ],
      compliances: ['APRA', 'MAS'],
      references: ['https://cloud.google.com/compute/docs/instances/changing-machine-type-of-stopped-instance'],
    },
    vm_underutilized: {
      title: 'Underutilized VM Instance',
      description:
        'This virtual machine appears to be underutilized based on CPU and memory metrics. Consider downsizing to a smaller instance type to reduce costs while maintaining adequate performance.',
      recommendations: [
        'Review the instance utilization metrics and consider downsizing to a smaller instance type that better matches actual workload requirements.',
      ],
    },
    vm_idle: {
      title: 'Idle VM Instance',
      description:
        'This virtual machine shows minimal or no activity over the monitoring period. An idle instance continues to incur compute costs without providing value.',
      recommendations: [
        'Investigate why the instance is idle. If no longer needed, terminate it. If needed intermittently, consider using auto-scaling or scheduled start/stop.',
      ],
    },
    vm_stopped: {
      title: 'Stopped VM Instance',
      description:
        'This virtual machine is in a stopped state but still incurs storage costs for attached volumes. Consider terminating if no longer needed.',
      recommendations: ['If the instance is no longer needed, terminate it and delete associated volumes to eliminate ongoing storage costs.'],
    },
    orphaned_volume: {
      title: 'Orphaned Volume',
      description: 'This storage volume is not attached to any virtual machine. Unattached volumes incur storage costs without providing value.',
      recommendations: [
        'Snapshot the volume if data needs to be preserved, then delete the unattached volume to eliminate unnecessary storage costs.',
      ],
    },
    storage_class_optimization: {
      title: 'Storage Class Optimization',
      description:
        'This storage resource may benefit from a different storage class based on its access patterns. Optimizing storage class can reduce costs.',
      recommendations: ['Review access patterns and consider moving to a more cost-effective storage class.'],
    },
    unused_load_balancer: {
      title: 'Unused Load Balancer',
      description: 'This load balancer has no healthy backend targets registered. It continues to incur costs without distributing any traffic.',
      recommendations: ['Remove unused load balancers to eliminate unnecessary costs, or register appropriate backend targets.'],
    },
    unassociated_public_ip: {
      title: 'Unassociated Public IP',
      description: 'This public IP address is not associated with any resource. Unassociated public IPs incur costs without providing value.',
      recommendations: ['Release unassociated public IP addresses to reduce costs, or associate them with the appropriate resources.'],
    },
  },
};
