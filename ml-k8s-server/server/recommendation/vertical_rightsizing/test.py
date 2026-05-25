import asyncio
from server.recommendation.vertical_rightsizing import generate_and_process_recommendation

if __name__ == "__main__":
    asyncio.run(
        generate_and_process_recommendation(
            "6d79c39f-920e-4167-85cf-e2ee83dcbc03", "e9dc2b39-78d3-46da-9691-f160bd3c4c19"
        )
    )
