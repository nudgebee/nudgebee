insert into integration_categories(value) values('incident_webhook') on conflict(value) do nothing;
insert into integration_types(name, category) values('pagerduty_webhook', 'incident_webhook') on conflict(name) do nothing;


create table if not exists event_incoming_webhooks
(
	id uuid primary key default gen_random_uuid(),
	tenant_id uuid not null,
	account_id uuid not null,
	integration_id uuid not null,
	integration_type text not null,
	webhook_id text not null,
	event_type text not null,
	event_id text not null,
	event_url text,
	event_status text,
	event_priority text,
	event_created_at timestamp not null,
	event_title text not null,
	event_description text,
	raw text
);

ALTER TABLE event_incoming_webhooks DROP CONSTRAINT IF EXISTS eventincomingwebhooks_tenantid_fkey;
ALTER TABLE event_incoming_webhooks ADD CONSTRAINT eventincomingwebhooks_tenantid_fkey FOREIGN KEY (tenant_id) REFERENCES tenant(id);


ALTER TABLE event_incoming_webhooks DROP CONSTRAINT IF EXISTS eventincomingwebhooks_accountid_fkey;
ALTER TABLE event_incoming_webhooks ADD CONSTRAINT eventincomingwebhooks_accountid_fkey FOREIGN KEY (account_id) REFERENCES cloud_accounts(id);

ALTER TABLE event_incoming_webhooks DROP CONSTRAINT IF EXISTS eventincomingwebhooks_integrationid_fkey;
ALTER TABLE event_incoming_webhooks ADD CONSTRAINT eventincomingwebhooks_integrationid_fkey FOREIGN KEY (integration_id) REFERENCES integrations(id);


ALTER TABLE event_incoming_webhooks DROP CONSTRAINT IF EXISTS eventincomingwebhooks_integrationtype_fkey;
ALTER TABLE event_incoming_webhooks ADD CONSTRAINT eventincomingwebhooks_integrationtype_fkey FOREIGN KEY (integration_type) REFERENCES integration_types(name);