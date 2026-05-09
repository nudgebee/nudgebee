import logging
from datetime import datetime, timedelta

from controllers.base import BaseController

WINDOW_SIZE = 60 * 24  # in minutes

logger = logging.getLogger(__name__)


class OpenCostAtrributesController(BaseController):
    @staticmethod
    def validate_parameters(**kwargs):
        pass

    def get_window(self):
        final_window = self.final_window
        if final_window:
            return f"{int(self.initial_window.timestamp())},{int(final_window.timestamp())}"
        else:
            return "0,0"

    @property
    def final_window(self):
        final_time = datetime.combine(self.initial_window.date(), datetime.max.time())
        if final_time.replace(microsecond=0) == self.initial_window:
            final_time = self.initial_window + timedelta(minutes=WINDOW_SIZE)
        if final_time < datetime.now():
            return final_time
        return datetime.now()

    def get_steps(self):
        final_steps = "1d"
        return final_steps

    def get(self, account_id, **kwargs):
        open_cost_arg = {}
        self.validate_parameters(**kwargs)
        # hardcoded for time being
        self.initial_window = (
            self.get_agent_last_synced_from_db(account_id=account_id) + timedelta(seconds=1)
        ).replace(microsecond=0)
        logger.info(f"Got initial window as {self.initial_window}")
        open_cost_arg["step"] = self.get_steps()
        open_cost_arg.update({"window": self.get_window()})
        logger.info(f"Sending arguments {open_cost_arg}")
        return open_cost_arg
