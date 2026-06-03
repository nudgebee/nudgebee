
CREATE  INDEX "spends_cloud_account_exclud_aggregate" on
  "public"."spends" using btree ("cloud_account", "exclude_aggregate");
