import os
import logging
import boto3
import botocore

from server.model_store.modelstore import ModelStore
from server.utils.utils import get_trace

logger = logging.getLogger(__name__)


class AWSS3Store(ModelStore):
    def __init__(self, bucket_name: str, aws_access_key=None, aws_secret_key=None):
        self.aws_access_key = aws_access_key
        self.aws_secret_key = aws_secret_key
        self.bucket_name = bucket_name

    def is_present(self, path: str) -> bool:
        s3 = boto3.client("s3")
        response = s3.list_objects_v2(Bucket=self.bucket_name, Prefix=path)
        logger.info("Deleting local file after uploading to s3")
        return "Contents" in response

    def save_model(self, model_name: str, source: str):
        with get_trace(__name__).start_as_current_span("save_model"):
            logger.info(f"Saving model {model_name} to AWS S3 bucket: {self.bucket_name}")
            s3 = boto3.resource("s3")
            try:
                s3.Bucket(self.bucket_name).upload_file(source, model_name)
                logger.info("Deleting local file after uploading to s3")
                os.remove(source)
            except botocore.exceptions.ClientError as e:
                msg = f"Failed to upload model: error {e}"
                logger.error(msg)
                raise ValueError(msg)

    def fetch_model(self, model_name: str, destination: str) -> bool:
        with get_trace(__name__).start_as_current_span("fetch_model"):
            logger.info(f"Fetching model {model_name} from AWS S3 bucket: {self.bucket_name}")
            s3 = boto3.resource("s3")
            try:
                s3.Bucket(self.bucket_name).download_file(model_name, destination)
                return True
            except botocore.exceptions.ClientError as e:
                msg = f"Failed to download model: {e}"
                if e.response.get("ResponseMetadata", {}).get("HTTPStatusCode", 0) == 404:
                    msg = "Failed to download model: (404) when calling the HeadObject operation: Not Found"
                logger.warning(msg)
                return False
