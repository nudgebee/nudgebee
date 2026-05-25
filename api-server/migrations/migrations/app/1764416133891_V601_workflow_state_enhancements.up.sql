CREATE TABLE IF NOT EXISTS workflow_state (
    workflow_id uuid NOT NULL,
    key VARCHAR(255) NOT NULL,
    value JSONB NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now(),
    expires_at TIMESTAMP WITHOUT TIME ZONE,
    last_updated_by_execution_id VARCHAR(255),
    last_updated_by_task_id VARCHAR(255),
    PRIMARY KEY (workflow_id, key),
    CONSTRAINT fk_workflow
      FOREIGN KEY(workflow_id) 
      REFERENCES workflows(id)
      ON DELETE CASCADE
);