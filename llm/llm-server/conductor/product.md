# Initial Concept
The LLM Server is a Go-based backend designed to orchestrate Large Language Model (LLM) operations, manage autonomous agents, and provide a toolkit for interacting with external systems (Datadog, K8s, etc.).

# Product Definition

## Primary Goal
The primary objective of the LLM Server is to provide a centralized LLM orchestration backend for Nudgebee services. It enables sophisticated agentic workflows and seamless tool integrations, serving as the "brain" for intelligent automation within the Nudgebee ecosystem.

## Target Users
The system is primarily built for end-users of the Nudgebee platform who interact with AI agents to troubleshoot infrastructure, analyze logs, or automate workflows. It also serves Nudgebee developers who build and refine these AI-powered capabilities.

## Key Features & Capabilities
*   **Hierarchical Agent Framework:** Utilizes both ReWOO (Reasoning WithOut Observation) for high-level planning and ReAct (Reason+Act) for task-specific execution.
*   **Multi-LLM Orchestration:** Integrated support for multiple providers including AWS Bedrock, Azure OpenAI, OpenAI, and Google Vertex AI, allowing for model flexibility and redundancy.
*   **Client-Augmented Tool Execution:** Supports dynamic tool registration from connected clients (e.g., CLI), allowing agents to delegate execution of local system tools (shell, file I/O) back to the user's environment.
*   **Comprehensive Infrastructure Toolkit:** Direct integration with observability platforms (Datadog, Prometheus, Loki, Elasticsearch) and cloud/container management tools (AWS, GCP, Kubernetes).
*   **Conversational & Knowledge-Enabled:** Supports long-running conversations and Retrieval Augmented Generation (RAG) to ground agent responses in specific technical context.
*   **Asynchronous Event Analysis:** Leverages RabbitMQ for handling complex, asynchronous investigation tasks and automated root-cause analysis (RCA).
