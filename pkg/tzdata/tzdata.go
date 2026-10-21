// Package tzdata embeds the IANA time zone database in the binary. A tenant may choose any IANA zone, and the services run in
// distroless images that have no /usr/share/zoneinfo, so every service that calls time.LoadLocation imports this package for its side effect.
package tzdata

import _ "time/tzdata" // the database (about 450 KB), used by time.LoadLocation when the host has none
