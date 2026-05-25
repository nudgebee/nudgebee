from controllers.base import BaseController
from handlers.playbook_handler import get_custom_playbooks


class PlaybookController(BaseController):

    def get_playbooks(self, tenant_id: str, cloud_account_id: str) -> list:
        return get_custom_playbooks(tenant_id, cloud_account_id)
