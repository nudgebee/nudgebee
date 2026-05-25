BEGIN TRANSACTION;
ALTER TABLE "public"."auto_pilot_approval_policy" DROP CONSTRAINT "autopilot_approval_policy_pkey";

ALTER TABLE "public"."auto_pilot_approval_policy"
    ADD CONSTRAINT "autopilot_approval_policy_pkey" PRIMARY KEY ("id");
COMMIT TRANSACTION;
