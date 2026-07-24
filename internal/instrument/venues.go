package instrument

import "github.com/upcomers-org/platformgo/internal/ids"

func VenueCBCM() ids.Venue { return ids.MustVenue("CBCM") }
func VenueGLBX() ids.Venue { return ids.MustVenue("GLBX") }
func VenueNYUM() ids.Venue { return ids.MustVenue("NYUM") }
func VenueXCBT() ids.Venue { return ids.MustVenue("XCBT") }
func VenueXCEC() ids.Venue { return ids.MustVenue("XCEC") }
func VenueXCME() ids.Venue { return ids.MustVenue("XCME") }
func VenueXFXS() ids.Venue { return ids.MustVenue("XFXS") }
func VenueXNYM() ids.Venue { return ids.MustVenue("XNYM") }

// CommonVenues returns a fresh ordered snapshot of the common venue constants.
func CommonVenues() []ids.Venue {
	return []ids.Venue{
		VenueCBCM(),
		VenueGLBX(),
		VenueNYUM(),
		VenueXCBT(),
		VenueXCEC(),
		VenueXCME(),
		VenueXFXS(),
		VenueXNYM(),
	}
}

// CommonVenueMap returns a fresh registry snapshot. Callers cannot mutate
// package-global state because the package holds no mutable venue registry.
func CommonVenueMap() map[string]ids.Venue {
	result := make(map[string]ids.Venue, 8)
	for _, venue := range CommonVenues() {
		result[venue.String()] = venue
	}
	return result
}

func LookupCommonVenue(code string) (ids.Venue, bool) {
	switch code {
	case "CBCM":
		return VenueCBCM(), true
	case "GLBX":
		return VenueGLBX(), true
	case "NYUM":
		return VenueNYUM(), true
	case "XCBT":
		return VenueXCBT(), true
	case "XCEC":
		return VenueXCEC(), true
	case "XCME":
		return VenueXCME(), true
	case "XFXS":
		return VenueXFXS(), true
	case "XNYM":
		return VenueXNYM(), true
	default:
		return "", false
	}
}
