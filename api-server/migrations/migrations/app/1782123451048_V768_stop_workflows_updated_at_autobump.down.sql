-- Restore the auto-bump trigger on workflows.updated_at (see V597).
create trigger set_public_workflows_updated_at before
update
    on
    public.workflows for each row execute function set_current_timestamp_updated_at();
