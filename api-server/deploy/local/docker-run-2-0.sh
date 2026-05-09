#! /bin/bash
docker run -d -p 8080:8080 \
	--env-file hasura/.env \
	hasura/graphql-engine:v2.15.1
