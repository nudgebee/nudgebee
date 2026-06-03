update auto_playbook_task set status = 'Scheduled' where status = 'SCHEDULED';
update auto_playbook_task set status = 'Created' where status = 'CREATED';
update auto_playbook_task set status = 'Complete' where status = 'COMPLETE';
update auto_playbook_task set status = 'Queued' where status = 'QUEUED';
update auto_playbook_task set status = 'Failed' where status = 'FAILED';
update auto_playbook_task set status = 'Skipped' where status = 'SKIPPED';


update auto_playbook set status = 'Active' where status = 'ACTIVE';
update auto_playbook set status = 'Disabled' where status = 'DISABLED';
