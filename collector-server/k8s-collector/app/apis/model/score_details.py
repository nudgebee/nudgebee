from dataclasses import dataclass

from apis.model.base_model import BaseDetails


@dataclass
class ScoreDetails(BaseDetails):
    best_practice_score: int = 0
    right_sizing_score: int = 0
