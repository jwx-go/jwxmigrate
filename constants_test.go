package main

// Shared string literals used across test files, extracted to satisfy
// goconst.
const (
	pkgDir         = "pkg"
	rootedPkgDir   = "./pkg"
	testGoFileName = "a.go"
	importRuleID   = "import-v3-to-v4"
	testRuleAID    = "rule-a"
	jwtWithFSCall  = "jwt.WithFS(fsys)"
)
