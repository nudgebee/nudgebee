generic_prompt = """
Score each document (0-1) based on how well it answers the user's question.

Scoring criteria (in priority order):
1. **Answer Relevancy** - Does it directly answer the question's intent?
2. **Correctness** - Is the information factually accurate?
3. **Specificity** - Is it specific to the question, not generic?
4. **Information Density** - Does it provide useful content concisely?

Rules:
- Each score must be unique
- Score based on content quality, NOT position in the list
- If no documents are relevant, return {{}}

Question:
{question}

Documents:
{documents}

Respond ONLY with a JSON object: {{ "0": 0.1, "1": 0.8, "2": 0.4 }}
"""

docs_prompt = """
Score each document (0-1) based on how well it answers the user's technical question.

Scoring criteria (in priority order):
1. **Answer Relevancy** - Does it directly help answer the question?
2. **Correctness** - Is the technical content accurate (CLI commands, config syntax, API behavior)?
3. **Specificity** - Is it specific to the exact question or use case?
4. **Information Density** - Does it deliver useful information concisely?

Rules:
- Each score must be unique
- Score based on content quality, NOT keyword overlap or position
- If no documents are relevant, return {{}}

Question:
{question}

Documents:
{documents}

Respond ONLY with a JSON object: {{ "0": 0.1, "1": 0.8, "2": 0.4 }}
"""

prometheus_prompt = """
Score each Prometheus document (0-1) based on how well the PromQL query answers the user's question.

Scoring criteria (in priority order):
1. **Query Relevance** - Does the PromQL expression address the user's intent?
2. **Metric Correctness** - Does it use the right metric and labels?
3. **Aggregation Logic** - Are aggregation functions and grouping appropriate?
4. **Syntax Validity** - Is the PromQL syntactically correct?

Rules:
- Each score must be unique
- Score based on query quality, NOT keyword matching or position
- If no documents are relevant, return {{}}

Question:
{question}

Documents:
{documents}

Respond ONLY with a JSON object: {{ "0": 0.1, "1": 0.8, "2": 0.4 }}
"""

events_prompt = """
Score each document (0-1) based on how well it answers the user's event-related question.

Scoring criteria (in priority order):
1. **Answer Relevancy** - Does it directly help answer the question?
2. **Correctness** - Is the information factually accurate and reliable?
3. **Specificity** - Is it specific to the exact question being asked?
4. **Information Density** - Does it provide useful content concisely?

Rules:
- Each score must be unique
- Score based on content quality, NOT keyword overlap or position
- If no documents are relevant, return {{}}

Question:
{question}

Documents:
{documents}

Respond ONLY with a JSON object: {{ "0": 0.1, "1": 0.8, "2": 0.4 }}
"""


module_prompts = {
    "docs": docs_prompt,
    "prometheus": prometheus_prompt,
    "events": events_prompt,
}


def get_prompt_for_module(module_name: str | None) -> str:
    if module_name:
        return module_prompts.get(module_name, generic_prompt)
    return generic_prompt
