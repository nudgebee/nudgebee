import logging
import os
import shutil

from server.model_store.modelstore import ModelStore
from server.utils.utils import get_trace

logger = logging.getLogger(__name__)


class FileStore(ModelStore):
    def __init__(self, base_path: str):
        self.base_path = base_path

    def is_present(self, path: str) -> bool:
        return os.path.exists(path)

    def save_model(self, model_name: str, source: str):
        with get_trace(__name__).start_as_current_span("save_model"):
            logger.info(f"Saving model {model_name} to  file storage path: {self.base_path}")
            try:
                destination = os.path.join(self.base_path, os.path.dirname(model_name))
                model_file_name = os.path.basename(model_name)
                os.makedirs(destination) if not os.path.exists(destination) else None
                shutil.copy(source, destination)
                logger.info(f"Saved model {model_file_name} to path : {destination}")
                logger.info(f"Deleting local model file after copying to filestore : {model_file_name}")
                os.remove(source)
            except Exception as e:
                msg = f"Failed to store model to file storage: {e}"
                logger.exception(msg)
                raise ValueError(msg)

    def fetch_model(self, model_name: str, destination: str):
        with get_trace(__name__).start_as_current_span("fetch_model"):
            logger.info(f"Fetching model : {model_name} from base path: {self.base_path}")
            try:
                source = os.path.join(self.base_path, model_name)
                shutil.copy(source, destination)
                logger.info(f"Fetched model from {source} to {destination}")
                return True
            except Exception as e:
                msg = f"Failed to fetch model {model_name} from base path {self.base_path}, {e}"
                logger.exception(msg)
                return False
