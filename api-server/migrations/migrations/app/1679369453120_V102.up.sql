
CREATE OR REPLACE VIEW "public"."recommendation_resourceservice" AS
    SELECT
      cloud_resourses.tenant,
      cloud_accounts.account_name,
      cloud_accounts.id,
      cloud_resourses.service_name,
      recommendation.category,
      recommendation.rule_name,
      sum(recommendation.estimated_savings) as estimated_savings,
      count(recommendation.*) as total_resources
    FROM cloud_resourses
    JOIN cloud_accounts ON ((cloud_resourses.account = cloud_accounts.id))
    JOIN recommendation ON ((recommendation.resource_id = cloud_resourses.id))
    GROUP BY cloud_resourses.tenant,
        cloud_accounts.id,
        cloud_accounts.account_name,
        cloud_resourses.service_name,
        recommendation.category,
        recommendation.rule_name;

DROP VIEW "public"."recommendation_resourceservice";

CREATE OR REPLACE VIEW "public"."recommendation_resourceservice" AS 
 SELECT cloud_resourses.tenant,
    cloud_accounts.id as account_id,
    cloud_accounts.account_name,
    cloud_resourses.service_name,
    recommendation.category,
    recommendation.rule_name,
    sum(recommendation.estimated_savings) AS estimated_savings,
    count(recommendation.*) AS total_resources
   FROM ((cloud_resourses
     JOIN cloud_accounts ON ((cloud_resourses.account = cloud_accounts.id)))
     JOIN recommendation ON ((recommendation.resource_id = cloud_resourses.id)))
  GROUP BY cloud_resourses.tenant, cloud_accounts.id, cloud_accounts.account_name, cloud_resourses.service_name, recommendation.category, recommendation.rule_name;
