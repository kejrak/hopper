// Package ui renders hopper's two-pane host picker: a fuzzy-filtered,
// sectioned host list on the left and host details on the right.
package ui

import (
	"sort"
	"strings"

	"github.com/kejrak/hopper/internal/host"
)

// item is one row of the left pane: either a section header or a host.
type item struct {
	header string     // non-empty → section header row
	h      *host.Host // non-nil → host row
}

// fuzzyMatch reports whether needle's bytes appear in hay in order,
// case-insensitively — "wpr" matches "web-prod". ASCII-oriented, which
// covers ssh host aliases.
func fuzzyMatch(needle, hay string) bool {
	needle = strings.ToLower(needle)
	hay = strings.ToLower(hay)
	i := 0
	for j := 0; j < len(hay) && i < len(needle); j++ {
		if needle[i] == hay[j] {
			i++
		}
	}
	return i == len(needle)
}

// matches applies the filter to a host's name, hostname and user.
func matches(h host.Host, filter string) bool {
	return fuzzyMatch(filter, h.Name) || fuzzyMatch(filter, h.Hostname) || fuzzyMatch(filter, h.User)
}

// buildItems assembles the sectioned rows: RECENT first (hosts named in
// recent, in that order), then one section per group — "default" first,
// the rest alphabetical. The filter applies to every section.
func buildItems(hosts []host.Host, recent []string, filter string) []item {
	byName := make(map[string]*host.Host, len(hosts))
	for i := range hosts {
		byName[hosts[i].Name] = &hosts[i]
	}

	var items []item
	var recentRows []item
	for _, name := range recent {
		if h, ok := byName[name]; ok && matches(*h, filter) {
			recentRows = append(recentRows, item{h: h})
		}
	}
	if len(recentRows) > 0 {
		items = append(items, item{header: "RECENT"})
		items = append(items, recentRows...)
	}

	groups := make(map[string][]*host.Host)
	for i := range hosts {
		h := &hosts[i]
		if matches(*h, filter) {
			groups[h.Group] = append(groups[h.Group], h)
		}
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		if g != "default" {
			names = append(names, g)
		}
	}
	sort.Strings(names)
	if _, ok := groups["default"]; ok {
		names = append([]string{"default"}, names...)
	}
	for _, g := range names {
		items = append(items, item{header: strings.ToUpper(g)})
		for _, h := range groups[g] {
			items = append(items, item{h: h})
		}
	}
	return items
}
