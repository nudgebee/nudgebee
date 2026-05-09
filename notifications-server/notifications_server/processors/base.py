class BaseProcessor:
    @staticmethod
    def validate_payload(payload):
        pass

    @staticmethod
    def create_tasks(event, payloads):
        raise NotImplementedError()

    @staticmethod
    def process_task(
        task,
    ):
        raise NotImplementedError()
