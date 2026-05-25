CREATE INDEX "spends_account_date_exclude" ON "public"."spends" USING btree ("cloud_account", "date", "exclude_aggregate") INCLUDE ("amount");
