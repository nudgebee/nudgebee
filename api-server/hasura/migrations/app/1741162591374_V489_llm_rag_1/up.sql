alter table llm_rags 
add column if not exists data_format varchar(100);

alter table llm_rags 
add column if not exists data_filename varchar(100);