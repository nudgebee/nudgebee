insert into event_rule_source
values ('AWS_CloudWatch_Alarm') on conflict do nothing;

insert into event_rule_source
values ('AWS_CloudTrail') on conflict do nothing;


insert into event_rule_source
values ('AWS_EventBridge') on conflict do nothing;





