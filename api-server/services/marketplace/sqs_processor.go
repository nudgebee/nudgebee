package marketplace

import (
	"context"
	"log/slog"
	"nudgebee/services/config"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func ProcessSqsMessagesForAwsMarketplace() {
	if config.Config.AwsMarketplacePurchaseUpdatesQueue == "" || config.Config.AwsMarketplaceSubscriptionStatusQueue == "" {
		slog.Info("No aws sqs queues for marketplace events found")
		return
	}

	creds := credentials.NewStaticCredentialsProvider(config.Config.AwsSellerAccessKey, config.Config.AwsSellerSecretKey, "")
	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(), awsconfig.WithRegion("us-east-1"), awsconfig.WithCredentialsProvider(creds))

	if err != nil {
		slog.Error("Error getting aws config:", "error", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	purchaseUpdatesMessages := make(chan sqstypes.Message, 10)
	subscriptionMessages := make(chan sqstypes.Message, 10)

	go pollSqs(ctx, cfg, config.Config.AwsMarketplacePurchaseUpdatesQueue, purchaseUpdatesMessages)
	go pollSqs(ctx, cfg, config.Config.AwsMarketplaceSubscriptionStatusQueue, subscriptionMessages)

	slog.Info("Listening on aws queues for marketplace events")

	go func() {
		for message := range purchaseUpdatesMessages {
			err := GetPurchaseDetailsAndUpdateEntitlements(&message)
			if err != nil {
				slog.Error("error updating purchase entitlements", "error", err)
				continue
			}
			deleteMessage(cfg, config.Config.AwsMarketplacePurchaseUpdatesQueue, &message)
		}
	}()

	go func() {
		for message := range subscriptionMessages {
			err := UpdateSubscriptionActions(&message)
			if err != nil {
				slog.Error("error charging customer", "error", err)
				continue
			}
			deleteMessage(cfg, config.Config.AwsMarketplaceSubscriptionStatusQueue, &message)
		}
	}()

	<-signalChan
	slog.Info("Received shutdown signal, terminating...")
	cancel()
}

func pollSqs(ctx context.Context, cfg aws.Config, queueUrl string, chn chan<- sqstypes.Message) {
	svc := sqs.NewFromConfig(cfg)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping polling for queue ", "queue name", queueUrl)
			close(chn)
			return
		default:
			output, err := svc.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
				QueueUrl:            aws.String(queueUrl),
				MaxNumberOfMessages: 10,
				WaitTimeSeconds:     10,
			})

			if err != nil {
				slog.Error("failed to fetch sqs message", "queueUrl", queueUrl, "error", err)
				time.Sleep(2 * time.Second) // Optional backoff before retrying
				continue
			}

			for _, message := range output.Messages {
				chn <- message
			}
		}
	}
}

func deleteMessage(cfg aws.Config, queueUrl string, message *sqstypes.Message) {
	svc := sqs.NewFromConfig(cfg)
	_, err := svc.DeleteMessage(context.TODO(), &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueUrl),
		ReceiptHandle: message.ReceiptHandle,
	})
	if err != nil {
		slog.Error("failed to delete sqs message", "queueUrl", queueUrl, "error", err)
		time.Sleep(1 * time.Second)
		_, retryErr := svc.DeleteMessage(context.TODO(), &sqs.DeleteMessageInput{
			QueueUrl:      aws.String(queueUrl),
			ReceiptHandle: message.ReceiptHandle,
		})
		if retryErr != nil {
			slog.Error("failed to delete sqs message after retry", "queueUrl", queueUrl, "error", retryErr)
		}
	}
}
