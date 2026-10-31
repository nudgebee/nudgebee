# Connect OpenObserve

Connect an OpenObserve organization to a Nudgebee account to query its logs,
metrics and traces. This guide covers the connection; it does not configure
telemetry collection in OpenObserve.

## Before you begin

- Have a Nudgebee account or Kubernetes cluster to associate with the
  integration, and an OpenObserve instance reachable from the Nudgebee API
  server. In a container, `localhost` refers to that container, not your host.
- Find the OpenObserve **organization identifier** in its organization list;
  this is distinct from the display name. See [OpenObserve's organization
  guide](https://openobserve.ai/docs/user-guide/account-administration/identity-and-access-management/organizations/).
- Obtain an OpenObserve username and password or service-account token with
  permission to list streams and query the data you need. See [OpenObserve's
  service-account guide](https://openobserve.ai/docs/user-guide/account-administration/identity-and-access-management/service-accounts/).
  OpenObserve Cloud does not support service accounts. Enterprise service
  accounts need assigned permissions; open-source service accounts have full
  access by default. An ingestion-only token cannot be used for general query
  APIs. Keep credentials out of source control, and use HTTPS outside a local
  test environment.

## Add the integration

1. In Nudgebee, open **Settings → Integrations** and select **OpenObserve**.
   Create an integration and associate it with the account or cluster to query.
2. Enter the base URL, organization identifier, username, and password or API
   token. Use the server origin, such as `https://openobserve.example.com`, without
   an `/api/<org>` path. The configured password/token is stored encrypted.
3. Enter the log and trace stream names if your organization uses names other
   than `default`. Stream names must contain only letters, digits and
   underscores; leaving either field blank uses `default` for that signal.
4. Select the default-provider options for logs, metrics or traces only where
   OpenObserve should be the account's default; these choices can replace the
   account's current provider for those signals.
5. Select **Test Connection**, review any stream notes, and save. The connection
   test checks the organization's streams endpoint; a successful test does not
   guarantee that every query or stream contains data.

## Supported signals

Nudgebee can query logs, metrics and traces from OpenObserve, Datadog and New
Relic. The available streams and query formats differ by provider.

| Signal  | OpenObserve                                                          | Datadog   | New Relic |
| ------- | -------------------------------------------------------------------- | --------- | --------- |
| Logs    | Supported; uses the configured log stream and OpenObserve search API | Supported | Supported |
| Metrics | Supported through PromQL; expression compatibility varies            | Supported | Supported |
| Traces  | Supported; uses the configured trace stream                          | Supported | Supported |

Choose the default provider separately for each signal. Queries are provider-
specific and may need translation when you switch sources.

## Verify queries

For the associated account, open its logs, metrics or traces view and choose a
time range containing data already present in OpenObserve. Query a known log,
metric or trace and compare the result with the same organization, stream and
time range in OpenObserve. Metrics use PromQL; logs and traces use OpenObserve's
search API. A successful connection test alone is not a query test.

OpenObserve alert **webhook ingestion** is configured separately. Use global page
search to open **OpenObserve Webhook**, create the integration, then follow the
form's destination URL and alert-template instructions. A webhook destination sends notifications;
it does not create a rule in OpenObserve. Nudgebee's OpenObserve alert-rule
management and broader service-map support are tracked in
[#499](https://github.com/nudgebee/nudgebee/issues/499).

## Troubleshooting

- **Connection refused or timeout:** verify that the URL is reachable from the
  API server, including container DNS, tunnel or port-forward settings.
- **HTTP 401 or 403:** check the username/token pair and the user's permissions
  in the selected organization. An ingestion token is not a query credential.
- **HTTP 404 on the streams endpoint:** check the base URL and organization
  identifier; do not append the API path to the base URL.
- **Connected but no logs or traces:** check the configured stream name, signal
  type and time range against OpenObserve. The connection test may display a
  note for a missing configured stream when other streams of that type exist;
  an organization with no streams of that type does not produce such a note.
- **No metrics:** confirm the metric exists in OpenObserve for the selected time
  range and use a query supported by its PromQL implementation. Some PromQL
  expressions supported by other providers may not work in OpenObserve.

Connection and query behavior can vary by OpenObserve version and deployment.
Report errors without including credentials or tokens.
