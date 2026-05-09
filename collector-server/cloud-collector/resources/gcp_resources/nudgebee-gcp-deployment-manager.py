"""
Nudgebee GCP Deployment Manager Template (Python)
Creates Pub/Sub infrastructure for Cloud Monitoring alert integration

Resources Created:
1. Pub/Sub Topic (customer project) - Receives Cloud Monitoring alerts
2. Pub/Sub Subscription (Nudgebee project) - Cross-project pull subscription
3. IAM Binding - Grants Nudgebee service account permission to subscribe
4. Notification Channel - Cloud Monitoring Pub/Sub channel with account token
"""

def GenerateConfig(context):
    """
    Generates GCP Deployment Manager configuration

    Args:
        context: Deployment Manager context with properties

    Returns:
        dict: Resources and outputs configuration
    """

    # Extract properties
    props = context.properties
    project_id = context.env['project']
    nudgebee_project_id = props.get('nudgebeeProjectId', 'nudgebee-prod')
    nudgebee_account_token = props.get('nudgebeeAccountToken')
    topic_name = props.get('topicName', 'nudgebee-cloud-monitoring-alerts')
    subscription_name = props.get('nudgebeeSubscriptionName', 'nudgebee-customer-alerts')

    # Nudgebee service account that will pull messages
    nudgebee_service_account = f"cloud-collector@{nudgebee_project_id}.iam.gserviceaccount.com"

    resources = []

    # 1. Create Pub/Sub Topic in customer's project
    topic_resource = {
        'name': topic_name,
        'type': 'pubsub.v1.topic',
        'properties': {
            'topic': topic_name,
            'labels': {
                'managed-by': 'nudgebee',
                'purpose': 'monitoring-alerts'
            }
        }
    }
    resources.append(topic_resource)

    # 2. Grant Nudgebee service account permission to create subscription
    topic_iam_binding = {
        'name': f'{topic_name}-iam-subscriber',
        'type': 'gcp-types/pubsub-v1:virtual.topics.iamMemberBinding',
        'properties': {
            'resource': topic_name,
            'role': 'roles/pubsub.subscriber',
            'member': f'serviceAccount:{nudgebee_service_account}'
        },
        'metadata': {
            'dependsOn': [topic_name]
        }
    }
    resources.append(topic_iam_binding)

    # 3. Create cross-project Pub/Sub subscription in Nudgebee's project
    # Note: This requires the deployment to have permissions in Nudgebee's project
    # Alternatively, this can be created by Nudgebee's infrastructure separately
    subscription_resource = {
        'name': f'{subscription_name}-{project_id}',
        'type': 'gcp-types/pubsub-v1:projects.subscriptions',
        'properties': {
            'subscription': f'projects/{nudgebee_project_id}/subscriptions/{subscription_name}-{project_id}',
            'topic': f'projects/{project_id}/topics/{topic_name}',
            'ackDeadlineSeconds': 60,
            'messageRetentionDuration': '604800s',  # 7 days
            'retryPolicy': {
                'minimumBackoff': '10s',
                'maximumBackoff': '600s'
            },
            'labels': {
                'customer-project': project_id,
                'nudgebee-account-token': nudgebee_account_token[:63] if nudgebee_account_token else 'unknown',
                'managed-by': 'nudgebee'
            },
            'filter': ''  # No filter, receive all messages
        },
        'metadata': {
            'dependsOn': [topic_name, f'{topic_name}-iam-subscriber']
        }
    }
    # Only create subscription if we have cross-project permissions
    # Otherwise, document that Nudgebee will create it
    # resources.append(subscription_resource)

    # 4. Create Cloud Monitoring Notification Channel (Pub/Sub)
    # This configures Cloud Monitoring to send alerts to the Pub/Sub topic
    notification_channel = {
        'name': 'nudgebee-pubsub-channel',
        'type': 'gcp-types/monitoring-v3:projects.notificationChannels',
        'properties': {
            'type': 'pubsub',
            'displayName': 'Nudgebee Alert Integration',
            'description': 'Sends Cloud Monitoring alerts to Nudgebee for analysis and remediation',
            'enabled': True,
            'labels': {
                'topic': f'projects/{project_id}/topics/{topic_name}'
            },
            'userLabels': {
                'nudgebee_account_token': nudgebee_account_token if nudgebee_account_token else '',
                'managed_by': 'nudgebee'
            }
        },
        'metadata': {
            'dependsOn': [topic_name]
        }
    }
    resources.append(notification_channel)

    # 5. Grant Cloud Monitoring permission to publish to the topic
    topic_publisher_iam = {
        'name': f'{topic_name}-iam-publisher',
        'type': 'gcp-types/pubsub-v1:virtual.topics.iamMemberBinding',
        'properties': {
            'resource': topic_name,
            'role': 'roles/pubsub.publisher',
            'member': 'serviceAccount:cloud-monitoring-notification@system.gserviceaccount.com'
        },
        'metadata': {
            'dependsOn': [topic_name]
        }
    }
    resources.append(topic_publisher_iam)

    # Outputs
    outputs = [
        {
            'name': 'topicName',
            'value': f'projects/{project_id}/topics/{topic_name}'
        },
        {
            'name': 'subscriptionName',
            'value': f'projects/{nudgebee_project_id}/subscriptions/{subscription_name}-{project_id}'
        },
        {
            'name': 'notificationChannelId',
            'value': '$(ref.nudgebee-pubsub-channel.name)'
        },
        {
            'name': 'setupInstructions',
            'value': f'''
Nudgebee GCP Integration Setup Complete!

Topic Created: projects/{project_id}/topics/{topic_name}
Notification Channel: Nudgebee Alert Integration

Next Steps:
1. Nudgebee will create a subscription in project {nudgebee_project_id}
2. Update your Cloud Monitoring alert policies to use this notification channel
3. Alerts will be automatically sent to Nudgebee for analysis

To add this channel to an existing alert policy:
gcloud alpha monitoring policies update [POLICY_NAME] \\
  --add-notification-channels=$(ref.nudgebee-pubsub-channel.name)

Account Token: {nudgebee_account_token[:8]}... (for support)
            '''
        }
    ]

    return {
        'resources': resources,
        'outputs': outputs
    }
