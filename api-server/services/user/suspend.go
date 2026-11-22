package user

import (
	"strings"
	"time"

	"nudgebee/services/security"
)

// userStatusSuspended is the users.status value that blocks sign-in.
const userStatusSuspended = "suspended"

// suspendedRevocationTTL is how long the revocation written on suspension is
// kept. A session issued before it can never be extended past it (NextAuth's jwt
// callback checks the denylist before re-issuing the cookie), so it only has to
// outlive the longest idle window a deployment can configure
// (NEXTAUTH_SESSION_EXPIRATION_DAYS). A year covers any sane setting, and there
// is one entry per suspended user.
const suspendedRevocationTTL = 366 * 24 * time.Hour

// revokeUserSessions records a user-wide session revocation: every session
// issued before now stops working. A variable so tests can observe the call
// without depending on which store holds the denylist.
var revokeUserSessions = func(ctx *security.RequestContext, userId string, expiresAt time.Time) error {
	_, err := DeleteSession(ctx, AuthDeleteSessionRequest{UserId: userId, ExpiresAt: expiresAt.Unix()})
	return err
}

// endSessionsIfSuspended ends every live session of userId when status is the
// suspended status. On its own a suspension only refuses the next sign-in: the
// gateway trusts a session cookie until it expires, and sessions slide, so a
// suspended user who kept using the app stayed signed in indefinitely.
func endSessionsIfSuspended(ctx *security.RequestContext, userId, status string) error {
	if !strings.EqualFold(status, userStatusSuspended) {
		return nil
	}
	return revokeUserSessions(ctx, userId, time.Now().Add(suspendedRevocationTTL))
}
