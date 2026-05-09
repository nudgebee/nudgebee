alter table cloud_account_usage_report rename column service_code to product_service_code;
alter table cloud_account_usage_report rename column region_code to resource_region_code;
alter table cloud_account_usage_report rename column operation to resource_operation;
alter table cloud_account_usage_report rename column category to cost_category;
alter table cloud_account_usage_report rename column sub_category to cost_sub_category;
alter table cloud_account_usage_report rename column currency to cost_currency;
alter table cloud_account_usage_report rename column tags to resource_tags;
