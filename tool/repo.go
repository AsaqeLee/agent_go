package tool

// RepoTools is a read-only code-repo sandbox (same path rules as the knowledge base).
// Names are list_repo / read_repo so they can coexist with docs tools.
func RepoTools(repoRoot string) []Tool {
	base := KnowledgeTools(repoRoot)
	if len(base) == 0 {
		return nil
	}
	var out []Tool
	for _, t := range base {
		switch t.Name() {
		case "list_docs":
			out = append(out, renameTool{
				Tool: t,
				name: "list_repo",
				desc: "List files in the sandboxed code repository. Paths cannot escape the repo root.",
			})
		case "read_doc":
			out = append(out, renameTool{
				Tool: t,
				name: "read_repo",
				desc: "Read one text file from the sandboxed code repository by relative path. Paths cannot escape the repo root.",
			})
		}
	}
	return out
}

type renameTool struct {
	Tool
	name, desc string
}

func (r renameTool) Name() string        { return r.name }
func (r renameTool) Description() string { return r.desc }
