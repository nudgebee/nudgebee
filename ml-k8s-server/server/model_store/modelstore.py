from abc import ABC, abstractmethod


class ModelStore(ABC):
    @abstractmethod
    def save_model(self, model_name: str, source: str):
        pass

    @abstractmethod
    def fetch_model(self, model_name: str, destination: str):
        pass

    @abstractmethod
    def is_present(self, path: str):
        pass
