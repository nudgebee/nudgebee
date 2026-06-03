import asyncio
from server.recommendation.vertical_rightsizing import generate_and_process_recommendation

if __name__ == "__main__":
    asyncio.run(
        generate_and_process_recommendation(
            "00000000-0000-0000-0000-000000000000", "11111111-1111-1111-1111-111111111111"
        )
    )
