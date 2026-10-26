package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/vieitesss/nexo/internal/statefile"
)

// projectIndex records the session nexo created or adopted for a project root.
// Rex has no native project metadata, so a mutable label cannot carry this
// identity and this document is the only durable mapping nexo owns.
type projectIndex struct {
	dir       string
	namespace string
}

type projectDocument struct {
	Roots map[string]string `json:"roots"`
}

func (b *Backend) projectIndex(ctx context.Context) (projectIndex, error) {
	resolved, err := b.server(ctx)
	if err != nil {
		return projectIndex{}, err
	}
	return projectIndex{dir: b.stateDir, namespace: "rex-projects-" + resolved.namespace()}, nil
}

func (p projectIndex) path() string {
	return statefile.Path(p.dir, p.namespace, "json")
}

func (p projectIndex) lockPath() string {
	return statefile.Path(p.dir, p.namespace, "lock")
}

// lookup returns the recorded session id for one project root. The caller
// verifies it against the live server before trusting it.
func (p projectIndex) lookup(root string) (string, bool, error) {
	document, err := p.read()
	if err != nil {
		return "", false, err
	}
	sessionID, ok := document.Roots[root]
	return sessionID, ok && sessionID != "", nil
}

// record stores the session for one project root. The read-modify-write runs
// under the shared state lock so concurrent opens cannot lose each other.
func (p projectIndex) record(root, sessionID string) error {
	release, err := statefile.Lock(p.lockPath(), false)
	if err != nil {
		return err
	}
	defer release()
	document, err := p.read()
	if err != nil {
		return err
	}
	if document.Roots == nil {
		document.Roots = map[string]string{}
	}
	document.Roots[root] = sessionID
	contents, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return statefile.WriteAtomic(p.path(), contents)
}

func (p projectIndex) read() (projectDocument, error) {
	contents, err := os.ReadFile(p.path())
	if errors.Is(err, os.ErrNotExist) {
		return projectDocument{}, nil
	}
	if err != nil {
		return projectDocument{}, err
	}
	var document projectDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		return projectDocument{}, fmt.Errorf("decode rex project metadata %s: %w", p.path(), err)
	}
	return document, nil
}
