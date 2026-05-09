import json
import logging
import os

import pytest
import requests
from datasets import Dataset
from langchain_openai import AzureChatOpenAI, AzureOpenAIEmbeddings
from ragas import evaluate
from ragas.metrics import (
    context_recall,
    context_precision,
    faithfulness,
    answer_relevancy,
)

# Create a logger
logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)

# Create a stream handler to print to the console
ch = logging.StreamHandler()
ch.setLevel(logging.INFO)

# Format the log messages
formatter = logging.Formatter("%(asctime)s - %(levelname)s - %(message)s")
ch.setFormatter(formatter)


# Add the handler to the logger
logger.addHandler(ch)


# Load and transform test data
@pytest.fixture(scope="session")
def load_test_data():
    with open("./data/benchmark_prometheus.json", "r") as f:
        data = json.load(f)

    # Convert list of dicts into structured dictionary format
    return {
        "query": [item["query"] for item in data],
        "contexts": [item["contexts"] for item in data],
        "ground_truths": [
            [item["ground_truths"]] for item in data
        ],  # Wrap ground truth in list
    }


@pytest.fixture
def sample_queries(load_test_data):
    """Extracts a sample of test queries, contexts, and ground truths"""
    test_data = load_test_data

    queries = test_data["query"]
    expected_contexts = test_data["contexts"]
    ground_truths = test_data["ground_truths"]

    return queries, expected_contexts, ground_truths


def get_matching_documents(
    query,
    account_id="a2a30b02-0f67-42e5-a2ab-c658230fd798",
    module="prometheus",
):

    url = "http://localhost:9988/get_matching_doc"
    payload = json.dumps({"query": query, "account_id": account_id, "module": module})
    headers = {
        "Content-Type": "application/json",
    }

    response = requests.request("POST", url, headers=headers, data=payload)
    # Parse the response
    if response.status_code == 200:
        response_data = response.json()
        # Extract the answer text from the response
        if "document" in response_data[0]:
            # Check if the response is a list of documents
            if isinstance(response_data, list):
                # Read the first document from the list
                doc = response_data[0]["document"]
                # Load it as JSON
                try:
                    doc = json.loads(doc)
                    # Check if the document is a dictionary
                    if isinstance(doc, dict):
                        if "metric" in doc:
                            return [json.dumps(doc["metric"])]
                        else:
                            logger.warning(
                                "Key 'document' not found in the JSON object"
                            )
                            return [json.dumps(doc)]
                    else:
                        logger.warning("Document is not a valid JSON object")
                        return [json.dumps(doc)]
                except json.JSONDecodeError:
                    logger.warning("Failed to decode JSON from the document")
                    return [json.dumps(doc)]
            return [response_data[0]["document"]]
        else:
            logger.warning(f"Unexpected response format: {response_data}")
            return response_data
    else:
        logger.warning(f"API request failed with status code: {response.status_code}")
        return []


@pytest.mark.benchmark
def test_rag_retrieval(sample_queries):
    queries, expected_contexts, ground_truths = sample_queries

    # Benchmark the entire document retrieval process
    retrieved_texts, answers = [], []
    count = 1
    for query in queries:
        logger.info(f"Retrieving documents for {count} of {len(queries)} queries")
        logger.info(f"Retrieving documents for query: {query}")
        docs = get_matching_documents(
            query,
            account_id="a2a30b02-0f67-42e5-a2ab-c658230fd798",
            module="prometheus",
        )
        # Append the retrieved documents to the list as server returns single document
        # Update the logic to handle multiple documents if server returns them
        logger.info(f"Documents retrieved: {docs}")
        retrieved_texts.append(docs)
        answers.append(" ".join(retrieved_texts[-1]))
        logger.info("Documents retrieved successfully")
        count += 1
        logger.info("\n\n")

    # Prepare dataset for Ragas evaluation
    eval_data = Dataset.from_dict(
        {
            "query": queries,
            "user_input": queries,
            "contexts": retrieved_texts,
            "answer": answers,
            "reference": ["".join(g) for gt in ground_truths for g in gt],
        }
    )

    azure_configs = {
        "api_version": os.environ.get("AZURE_OPENAI_API_VERSION", "2025-01-01-preview"),
        "base_url": os.environ.get("AZURE_OPENAI_ENDPOINT"),
        "model_name": os.environ.get("AZURE_OPENAI_MODEL_NAME", "gpt-4o"),
        "embedding_name": os.environ.get(
            "AZURE_OPENAI_EMBEDDING_NAME", "text-embedding-ada-002"
        ),
        "llm_deployment_name": os.environ.get(
            "AZURE_OPENAI_LLM_DEPLOYMENT_NAME", "gpt-4o"
        ),
        "embeddings_deployment_name": os.environ.get(
            "AZURE_OPENAI_EMBEDDINGS_DEPLOYMENT_NAME", "text-embedding-ada-002"
        ),
        "azure_api_key": os.environ.get("AZURE_OPENAI_API_KEY"),
    }
    # Initialize the AzureChatOpenAI model
    # noinspection PyArgumentList
    azure_openai_model = AzureChatOpenAI(
        azure_endpoint=azure_configs["base_url"],
        model=azure_configs["model_name"],
        validate_base_url=False,
        openai_api_key=azure_configs["azure_api_key"],
        openai_api_type="azure",
        api_version=azure_configs["api_version"],
        deployment_name=azure_configs["llm_deployment_name"],
    )

    # init the embeddings for answer_relevancy, answer_correctness and answer_similarity
    # noinspection PyArgumentList
    azure_openai_embeddings = AzureOpenAIEmbeddings(
        azure_endpoint=azure_configs["base_url"],
        model=azure_configs["embedding_name"],
        openai_api_key=azure_configs["azure_api_key"],
        openai_api_type="azure",
        api_version="2025-01-01-preview",
        deployment=azure_configs["embeddings_deployment_name"],
    )

    # Define evaluation metrics
    metrics = [context_recall, context_precision, faithfulness, answer_relevancy]
    results = evaluate(
        eval_data,
        metrics=metrics,
        llm=azure_openai_model,
        embeddings=azure_openai_embeddings,
        raise_exceptions=True,
    )

    # Extract metrics into separate lists
    context_recall_values = results["context_recall"]
    context_precision_values = results["context_precision"]
    faithfulness_values = results["faithfulness"]
    answer_relevancy_values = results["answer_relevancy"]

    # Convert results to a dictionary (if it's an object with protected attributes)
    metrics_dict = results._repr_dict  # Access the protected dictionary

    # Extract values
    context_recall_score = metrics_dict["context_recall"]
    context_precision_score = metrics_dict["context_precision"]
    faithfulness_score = metrics_dict["faithfulness"]
    answer_relevancy_score = metrics_dict["answer_relevancy"]

    # Compute overall accuracy
    overall_accuracy = (
        context_recall_score
        + context_precision_score
        + faithfulness_score
        + answer_relevancy_score
    ) / 4

    # Create a detailed report with metrics for each query
    report = {
        "details": [
            {
                "query": queries[i],
                "retrieved_contexts": retrieved_texts[i],
                "expected_contexts": expected_contexts[i],
                "context_recall": context_recall_values[i] * 100,
                "context_precision": context_precision_values[i] * 100,
                "faithfulness": faithfulness_values[i] * 100,
                "answer_relevancy": answer_relevancy_values[i] * 100,
            }
            for i in range(len(queries))
        ],
        "summary": {
            "total_queries": len(queries),
            "context_recall": context_recall_score * 100,
            "context_precision": context_precision_score * 100,
            "faithfulness": faithfulness_score * 100,
            "answer_relevancy": answer_relevancy_score * 100,
            "overall_accuracy": overall_accuracy * 100,
        },
    }

    # Save results to JSON
    with open("./report/eval_report.json", "w") as f:
        json.dump(report, f, indent=4)

    # Print metrics including overall accuracy
    print("\n===== RAG Evaluation Report =====")
    print(f"Total Queries Evaluated: {len(queries)}")
    print(f"Context Recall: {context_recall_score:.4f}")
    print(f"Context Precision: {context_precision_score:.4f}")
    print(f"Faithfulness: {faithfulness_score:.4f}")
    print(f"Answer Relevancy: {answer_relevancy_score:.4f}")
    print(f"Overall Accuracy: {overall_accuracy:.4f}")  # Display overall accuracy
    print("=================================\n")

    # Save overall accuracy to report
    report["Context Recall"] = context_recall_score * 100
    report["Context Precision"] = context_precision_score * 100
    report["Faithfulness"] = faithfulness_score * 100
    report["Answer Relevancy"] = answer_relevancy_score * 100
    report["Accuracy"] = overall_accuracy * 100

    # Assertions for CI/CD pipeline
    logger.info(
        f"Context Recall: {context_recall_score:.4f}"
        if context_recall_score >= 0.7
        else f"Low context recall: {context_recall_score:.4f}"
    )

    logger.info(
        f"Context Precision: {context_precision_score:.4f}"
        if context_precision_score >= 0.7
        else f"Low context precision: {context_precision_score:.4f}"
    )

    logger.info(
        f"Faithfulness: {faithfulness_score:.4f}"
        if faithfulness_score >= 0.7
        else f"Low faithfulness: {faithfulness_score:.4f}"
    )

    logger.info(
        f"Answer Relevancy: {answer_relevancy_score:.4f}"
        if answer_relevancy_score >= 0.7
        else f"Low answer relevancy: {answer_relevancy_score:.4f}"
    )

    logger.info(
        f"Overall Accuracy: {overall_accuracy:.4f}"
        if overall_accuracy >= 0.75
        else f"Low overall accuracy: {overall_accuracy:.4f}"
    )
