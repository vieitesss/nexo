package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/vieitesss/nexo/internal/statefile"
)

// metadataIndex is the one sidecar document nexo owns for a Rex server run.
// Rex has no native project or proxy metadata, so a mutable label cannot carry
// either identity and this document is the only durable mapping nexo owns.
type metadataIndex struct {
	dir       string
	namespace string
}

// metadataDocument maps nexo-owned state to native ids. Roots records the
// session opened or adopted for a project root; Proxies records the machine and
// remote multiplexer of a proxy session.
type metadataDocument struct {
	Roots   map[string]string      `json:"roots,omitempty"`
	Proxies map[string]proxyRecord `json:"proxies,omitempty"`
}

// proxyRecord is the immutable identity of one machine proxy. Nothing here is
// derived from a session label: the label stays display data.
type proxyRecord struct {
	Machine    string `json:"machine"`
	RemoteKind string `json:"remote_kind,omitempty"`
}

func (b *Backend) metadata(ctx context.Context) (metadataIndex, error) {
	resolved, err := b.server(ctx)
	if err != nil {
		return metadataIndex{}, err
	}
	return metadataIndex{dir: b.stateDir, namespace: "rex-metadata-" + resolved.namespace()}, nil
}

func (m metadataIndex) path() string {
	return statefile.Path(m.dir, m.namespace, "json")
}

func (m metadataIndex) lockPath() string {
	return statefile.Path(m.dir, m.namespace, "lock")
}

// lookupRoot returns the recorded session id for one project root. The caller
// verifies it against the live server before trusting it.
func (m metadataIndex) lookupRoot(root string) (string, bool, error) {
	document, err := m.read()
	if err != nil {
		return "", false, err
	}
	sessionID, ok := document.Roots[root]
	return sessionID, ok && sessionID != "", nil
}

// recordRoot stores the session for one project root.
func (m metadataIndex) recordRoot(root, sessionID string) error {
	return m.update(func(document *metadataDocument) bool {
		if document.Roots == nil {
			document.Roots = map[string]string{}
		}
		document.Roots[root] = sessionID
		return true
	})
}

// proxies returns a copy of every recorded proxy, keyed by native session id.
func (m metadataIndex) proxies() (map[string]proxyRecord, error) {
	document, err := m.read()
	if err != nil {
		return nil, err
	}
	proxies := make(map[string]proxyRecord, len(document.Proxies))
	for sessionID, record := range document.Proxies {
		proxies[sessionID] = record
	}
	return proxies, nil
}

// lookupProxy reports the proxy identity recorded for one native session id.
func (m metadataIndex) lookupProxy(sessionID string) (proxyRecord, bool, error) {
	document, err := m.read()
	if err != nil {
		return proxyRecord{}, false, err
	}
	record, ok := document.Proxies[sessionID]
	return record, ok, nil
}

// recordProxy stores the machine identity of one proxy session. A later switch
// rewrites the record so the remembered remote kind stays truthful.
func (m metadataIndex) recordProxy(sessionID string, record proxyRecord) error {
	return m.update(func(document *metadataDocument) bool {
		if document.Proxies == nil {
			document.Proxies = map[string]proxyRecord{}
		}
		document.Proxies[sessionID] = record
		return true
	})
}

// update runs one read-modify-write under the shared state lock so concurrent
// opens cannot lose each other.
func (m metadataIndex) update(mutate func(*metadataDocument) bool) error {
	release, err := statefile.Lock(m.lockPath(), false)
	if err != nil {
		return err
	}
	defer release()
	document, err := m.read()
	if err != nil {
		return err
	}
	if !mutate(&document) {
		return nil
	}
	contents, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return statefile.WriteAtomic(m.path(), contents)
}

func (m metadataIndex) read() (metadataDocument, error) {
	contents, err := os.ReadFile(m.path())
	if errors.Is(err, os.ErrNotExist) {
		return metadataDocument{}, nil
	}
	if err != nil {
		return metadataDocument{}, err
	}
	var document metadataDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		return metadataDocument{}, fmt.Errorf("decode rex metadata %s: %w", m.path(), err)
	}
	return document, nil
}
