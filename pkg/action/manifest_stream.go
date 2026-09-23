/*
Copyright The Helm Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package action

import (
	"sort"
	"strings"

	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"
)

type streamDoc struct {
	source string
	index  int
	text   string
}

// manifestStream collects documents for the unified manifest stream, which is
// ordered by Source path and, within a Source, by rendered position.
type manifestStream struct {
	docs []streamDoc
}

func (s *manifestStream) add(source string, index int, text string) {
	s.docs = append(s.docs, streamDoc{source: source, index: index, text: text})
}

func (s *manifestStream) String() string {
	docs := make([]streamDoc, len(s.docs))
	copy(docs, s.docs)
	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].source != docs[j].source {
			return docs[i].source < docs[j].source
		}
		return docs[i].index < docs[j].index
	})

	var b strings.Builder
	for _, d := range docs {
		b.WriteString("---\n")
		b.WriteString(strings.TrimSpace(d.text))
		b.WriteString("\n")
	}
	return b.String()
}

// renderedPositions records where each document appeared within its rendered
// template file, so that documents can be put back in rendered order after
// hooks and manifests have been separated and sorted by kind.
type renderedPositions map[string][]int

func newRenderedPositions(files map[string]string) renderedPositions {
	p := renderedPositions{}
	for name, content := range files {
		split := releaseutil.SplitManifests(content)
		keys := make([]string, 0, len(split))
		for k := range split {
			keys = append(keys, k)
		}
		sort.Sort(releaseutil.BySplitManifestsOrder(keys))
		for i, k := range keys {
			key := positionKey(name, split[k])
			p[key] = append(p[key], i)
		}
	}
	return p
}

// take returns the rendered position of a document. Identical documents in the
// same file are handed out in rendered order.
func (p renderedPositions) take(name, content string) int {
	key := positionKey(name, content)
	positions := p[key]
	if len(positions) == 0 {
		return 0
	}
	p[key] = positions[1:]
	return positions[0]
}

func positionKey(name, content string) string {
	return name + "\x00" + content
}
