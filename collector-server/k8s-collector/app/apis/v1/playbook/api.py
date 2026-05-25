from apis.base.api import BaseAuthApi
from controllers.playbook_controller import PlaybookController
from flask import request


class PlaybookHandler(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def _get_controller_class(self):
        return PlaybookController

    def get(self):
        cloud_account_id = request.cloud_account_id
        tenant = request.tenant
        return self.build_response(self.controller.get_playbooks(tenant, cloud_account_id))
