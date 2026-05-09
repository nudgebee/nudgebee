alter table cloud_account_usage_report rename column product_service_code to service_code;
alter table cloud_account_usage_report rename column resource_region_code to region_code;
alter table cloud_account_usage_report rename column resource_operation to operation;
alter table cloud_account_usage_report rename column cost_category to category;
alter table cloud_account_usage_report rename column cost_sub_category to sub_category;
alter table cloud_account_usage_report rename column cost_currency to currency;
alter table cloud_account_usage_report rename column resource_tags to tags;
