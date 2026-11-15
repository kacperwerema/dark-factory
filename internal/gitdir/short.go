package gitdir

// shortSHALen is the one length of a shortened commit hash.
const shortSHALen = 7

// ShortSHA is the form a commit hash is shown in: sha cut to 7 characters, or
// as it is when shorter. For showing only: a value stored or compared keeps
// the full hash (TestOneShortSHA).
func ShortSHA(sha string) string {
	if len(sha) > shortSHALen {
		return sha[:shortSHALen]
	}
	return sha
}
