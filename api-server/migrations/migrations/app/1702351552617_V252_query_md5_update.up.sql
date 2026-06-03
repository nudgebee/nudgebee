
update dw_queries
set query_md5 = md5(query_text)
where query_md5 is null;
