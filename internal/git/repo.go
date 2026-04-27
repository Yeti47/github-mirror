package git

// Repo represents a GitHub repository to be mirrored.
type Repo struct {
	// FullName is the owner/name identifier, e.g. "octocat/Hello-World".
	FullName string
	// CloneURL is the HTTPS clone URL without embedded credentials.
	CloneURL string
	// Archived indicates the repository is archived on GitHub.
	// Archived repos are still mirrored — they are valuable for backup.
	Archived bool
}
