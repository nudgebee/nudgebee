from notifications_server.models.enums import ReactionTypes
from notifications_server.processors.email import EmailProcessor


class ProcessorFactory:
    processors = {ReactionTypes.EMAIL: EmailProcessor}

    @classmethod
    def get(cls, reaction_type):
        return cls.processors.get(reaction_type)
