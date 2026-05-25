import logging

from datasets import Dataset
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel
from ragas import evaluate
from ragas.metrics import answer_relevancy

from ..common.llm import get_llm, get_embeddings

router = APIRouter()
logger = logging.getLogger(__name__)


class RagasInput(BaseModel):
    query: str
    answer: str


@router.post("/score")
async def calculate_ragas_metrics(input_data: RagasInput):
    if not input_data.query or not input_data.answer:
        raise HTTPException(
            status_code=400,
            detail="Both 'query' and 'answer' fields are required.",
        )
    data = {
        "query": [input_data.query],
        "answer": [input_data.answer],
        "user_input": [input_data.query],
    }
    llm = get_llm()
    embeddings = get_embeddings()
    try:
        logger.info(f"Calculating RAGAS metrics for query: {input_data.query}")
        dataset = Dataset.from_dict(data)
        result = evaluate(
            dataset,
            llm=llm,
            embeddings=embeddings,
            metrics=[answer_relevancy],
            raise_exceptions=True,
        )
        answer_relevancy_values = result["answer_relevancy"]
        logger.info(f"Raw answer relevancy values: {answer_relevancy_values}")
        score = answer_relevancy_values[0]
        if score is not None:
            score = max(0, min(100, round(score * 100)))
        else:
            score = 0
        logger.info(f"Calculated RAGAS score: {score}, for query: {input_data.query}")
        return {"score": score}
    except Exception as e:
        logger.error(f"Error calculating RAGAS metrics: {e}")
        raise HTTPException(status_code=500, detail=str(e))
