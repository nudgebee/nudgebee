# Getting Started with Next JS

This project was bootstrapped with [Create Next App](https://nextjs.org/). Major libraries the project uses [Material UI](https://mui.com/material-ui/getting-started/), Typescript, [GraphQL](https://graphql.org/), [next-auth](https://next-auth.js.org/getting-started/introduction) and [vis-network](https://github.com/visjs/vis-network).

## Prerequisites

Before you begin, make sure you have the following installed:

- Node.js: Next.js requires Node.js to run. You can download (version 21.x) it from [here](https://nodejs.org/).
- npm (Node Package Manager): npm comes with Node.js installation.

## Setup the app project:

1. Navigate to the app folder from the terminal.
2. Execute `npm install --legacy-peer-deps`
3. Add required environment variables in `.env` files.
4. Run `npm run dev`

## Available Scripts

In the project directory, you can run:

### `npm run dev`

Runs the app in the development mode.\
Open [http://localhost:3000](http://localhost:3000) to view it in the browser.

The page will reload if you make edits.\
You will also see any lint errors in the console.

### `npm test`

Launches the test runner in the interactive watch mode.\

### `npm run build --legacy-peer-deps --only=production`

Builds the app for production to the `build` folder.\
It correctly bundles React in production mode and optimizes the build for the best performance.

The build is minified and the filenames include the hashes.\
Your app is ready to be deployed!

---

## Development Context

### Project Overview

The App is a frontend application built with **Next.js** (v16) and **React** (v18.2). It uses **Turbopack** for the development server and **standalone output mode** for production container builds.

### Key Technologies

- **Styling:** Material UI (@mui/material), Emotion, SASS, Tailwind CSS.
- **State Management & Data Fetching:** React Hook Form, Axios, SWR.
- **Charts & Visuals:** Chart.js, D3, React Flow, XTerm.js.
- **Editors:** CodeMirror (JS, JSON, Markdown, YAML support).
- **Auth:** NextAuth.js.

### Development Conventions

- **GraphQL API Layer**: All backend communication is GraphQL-shaped, parsed and dispatched in-app by `@lib/rpcGateway` to upstream service handlers. Modules follow the `queryGraphQL` pattern in `HttpService` with TypeScript interfaces for requests.
- **Path Aliases**: Use `@api1`, `@components1`, `@common`, `@lib`, `@hooks`, `@context`, `@data`, `@assets`, and `@utils` as defined in `tsconfig.json`.
- **Authentication & Authorization**: Protected routes use the `withAuth` HOC. Permission utilities (`hasReadAccess`, `isTenantAdmin`, etc.) are in `@lib/auth`.
- **Code Quality:** Linting via `next lint` and `oxlint`. Formatting with Prettier (`npm run prettier:check`).
- **Testing:** Unit tests with Jest and React Testing Library.
- **Type Safety:** Use strictly typed TypeScript.
- **Testing IDs**: Always include `id` or `data-testid` for automation testing on clickable elements.
- **Design System:** Reference [`design-system.md`](design-system.md) for components and typography.
