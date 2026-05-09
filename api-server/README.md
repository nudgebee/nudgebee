# API Server (GraphQL)

## Description
- This Repo hosts, GraphQL based API layer for frontend applications.
- Currently it uses __[Hasura](https://hasura.io/)__ for providing GraphQL APIs
- GraphQL APIs are stateless, and we need to provide bearer/jwt tokens to authenticate (apart from admin tokens)
- `actions` are used to extend functionality of hasura

## Technologies
- [Hasura](https://hasura.io/)
- [Docker](https://docker.io)
- [Postgres](https://www.postgresql.org/)
- [express](https://expressjs.com/)


## Running (Local)
- By Default local setup assumes postgres/postgres for username/password for local instance
- Start GraphQL server Docker Image
- in local mode while starting server/console configs are read from `.env` file, update this file for any DB/config related changes

``` 
bash dist/local/docker-run-2-0.sh
```

- Do data migration and metadata update

```
cd hasura

hasura metadata apply --log-level DEBUG

hasura migrate apply --database-name app --log-level DEBUG

hasura metadata reload

hasura console
```

- you can directly open __hasura__ admin console on http://localhost:8080 though preferred is to use url printed by `hasura console` command as it also takes care of any data migration/schema updates

- If there are DB changes because of hasura then use squash
```
hasura migrate status
hasura migrate squash --from <> --to <> --name <>
```


## Authentication
- Current setup is configured to work with JWT bearer tokens returned by amazon cognito pool
    - Start UI and Hit token option, it will return IDToken or Get Tokens directly from Cognito UI
- Alternative authentication is using admin token as header (`x-hasura-admin-secret`)

## API endpoints
- GraphQL -  `http://localhost:8080/v1/graphql`


# Actions

## Running Dev Server

```
npm run dev
```

## Running Lint and Fixing

```
npm run lint
```

```
npm run lint:fix
```

## Adding a new route

- Add new file in handlers
- filename will be treated as route name
- handler should export default function which will be used to route requests








