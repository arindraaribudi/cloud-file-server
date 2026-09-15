package admin

import "strings"

// friendlyCOSErr maps Tencent COS SDK errors into messages an admin can act on.
// When the underlying error looks like an auth/permission failure, returns a
// single line that names the env vars to check; otherwise includes the raw
// error so the admin can debug.
func friendlyCOSErr(op string, err error) string {
	if err == nil {
		return op + ": ok"
	}
	s := err.Error()
	low := strings.ToLower(s)
	authy := strings.Contains(low, "unauthorized") ||
		strings.Contains(low, "accessdenied") ||
		strings.Contains(low, "forbidden") ||
		strings.Contains(low, "signaturedoesnotmatch") ||
		strings.Contains(low, "invalidaccesskeyid") ||
		strings.Contains(low, "nosuchbucket") ||
		strings.Contains(s, "403") ||
		strings.Contains(s, "401")
	if authy {
		return "COS auth/permission error during " + op +
			" — check COS_STATIC_SECRET_ID, COS_STATIC_SECRET_KEY, COS_BUCKET, COS_REGION. Detail: " + s
	}
	return op + ": " + s
}
