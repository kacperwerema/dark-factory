package testids

func init() {
	IDs = append(IDs,
		// The owner sign-in in place (static/owner-signin.js, its markup in
		// templates/_owner_signin.html): any page's refused owner decision.
		"owner_signin.dialog",
		"owner_signin.send",
		"owner_signin.code",
		"owner_signin.submit",
		"owner_signin.cancel",
		"owner_signin.msg",
	)
}
