package query

import (
	"fmt"
	"strings"
)

// applicationGroupAccountScope returns the AccountScope for an application-group
// table referenced as ref. application_group has no account column of its own —
// the account lives in application_group_mapping — so a group is visible when ANY
// of its mapped workloads belongs to an account the reader holds (deliberately not
// notification_rule_scope.go's ALL-contained rule: a group listing is informational,
// unlike a notification rule that can act on an account the reader can't see).
func applicationGroupAccountScope(ref string) func(accountIds []string, quote func(any) string) string {
	return func(accountIds []string, quote func(any) string) string {
		quoted := make([]string, 0, len(accountIds))
		for _, id := range accountIds {
			quoted = append(quoted, quote(id))
		}
		return fmt.Sprintf(
			"EXISTS (SELECT 1 FROM application_group_mapping agm WHERE agm.group_id = %[1]s.id::uuid AND agm.account_id IN (%[2]s))",
			ref, strings.Join(quoted, ", "),
		)
	}
}
