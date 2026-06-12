# Quick Start

The full local-development walkthrough — clone, bring up infra with Docker Compose, configure backend + frontend env, run the app, and sign in — lives in the root README:

➜ **[README → Quick Start (Local Development)](../README.md#quick-start-local-development)**

## TL;DR (after reading the README walkthrough)

```bash
git clone https://github.com/nudgebee/nudgebee.git && cd nudgebee
docker compose up -d postgres rabbitmq redis qdrant temporal migrations
cp api-server/services/.env.example api-server/services/.env  # edit as needed
cd api-server/services && make run
# in another shell:
cp app/.env.example app/.env  # edit as needed
cd app && npm install --legacy-peer-deps && npm run dev
```

Then open `http://localhost:3000` and follow the **Sign in** instructions in [README → step 7](../README.md#7-sign-in).

## When in doubt

- **What's the matching command for service X?** [README → Project Structure table](../README.md#project-structure) lists each service's run command.
- **Build failing locally?** [CONTRIBUTING.md → Troubleshooting](../CONTRIBUTING.md#troubleshooting) covers the common ones (`fetch failed`, Postgres `connection refused`, RabbitMQ stuck queues, npm peer deps, OOM on build, etc.).
- **Need to deploy this to a real cluster?** [README → Deploy to Kubernetes (Helm)](../README.md#deploy-to-kubernetes-helm).
