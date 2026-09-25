// Package audnexregion holds the supported Audnex regions. It depends only on
// the standard library so any layer can import it without creating cycles.
package audnexregion

var regions = [...]string{"us", "ca", "uk", "au", "de", "fr", "es", "in", "it", "jp"}

// Regions returns a copy of the supported Audnex regions in sweep order:
// us, ca, uk, au, de, fr, es, in, it, jp.
func Regions() []string {
	out := make([]string, len(regions))
	copy(out, regions[:])
	return out
}

// IsRegion reports whether region is one of the ten supported Audnex regions.
// The match is exact; callers normalize case and whitespace.
func IsRegion(region string) bool {
	for _, candidate := range regions {
		if region == candidate {
			return true
		}
	}
	return false
}
