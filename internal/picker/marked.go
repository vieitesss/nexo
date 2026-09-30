package picker

// MarkedContainerRefs returns marked container identities in source order.
func MarkedContainerRefs(places []Place, marked map[ContainerRef]bool) []ContainerRef {
	refs := make([]ContainerRef, 0)
	seen := make(map[ContainerRef]bool)
	for _, place := range places {
		ref := ContainerRef{Machine: place.Machine, ID: place.ContainerID}
		if place.Kind != ContainerKind || ref.ID == "" || !marked[ref] || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs
}
