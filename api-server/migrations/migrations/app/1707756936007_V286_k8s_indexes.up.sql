
CREATE  INDEX "events_cloudaccountid_subjecttype" on
  "public"."events" using btree ("cloud_account_id", "subject_type");

CREATE  INDEX "k8s_pods_cloudaccountid" on
  "public"."k8s_pods" using btree ("cloud_account_id");

CREATE  INDEX "k8s_pods_cloudaccountid_isactive" on
  "public"."k8s_pods" using btree ("cloud_account_id", "is_active");

CREATE  INDEX "k8s_nodes_cloudaccountid" on
  "public"."k8s_nodes" using btree ("cloud_account_id");

CREATE  INDEX "k8s_nodes_cloudaccountid_isactive" on
  "public"."k8s_nodes" using btree ("cloud_account_id", "is_active");
