
alter table "public"."application_profile" rename column "profile_tool" to "output_type";

alter table "public"."application_profile" add column "profile_tool" text
 null;
