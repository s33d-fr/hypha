//go:build windows

package inventory

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// vendorInfo lit le fabricant et le modèle dans la description BIOS du registre.
func vendorInfo() (vendor, product string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\BIOS`, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer k.Close()
	vendor, _, _ = k.GetStringValue("SystemManufacturer")
	product, _, _ = k.GetStringValue("SystemProductName")
	return vendor, product
}

// timezone rend le nom Windows du fuseau (« Romance Standard Time » pour Paris).
func timezone() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\TimeZoneInformation`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	tz, _, _ := k.GetStringValue("TimeZoneKeyName")
	return tz
}

// platformVersion rend la version de build de Windows, révision comprise
// (« 10.0.17763.6893 » pour un Windows Server 2019 à jour), lue dans
// HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion. Windows antérieurs à 10 :
// CurrentVersion (« 6.3 ») tient lieu de majeure.mineure.
func platformVersion() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	build, _, err := k.GetStringValue("CurrentBuildNumber")
	if err != nil || build == "" {
		build, _, _ = k.GetStringValue("CurrentBuild")
	}
	if build == "" {
		return ""
	}
	var version string
	major, _, errMaj := k.GetIntegerValue("CurrentMajorVersionNumber")
	minor, _, errMin := k.GetIntegerValue("CurrentMinorVersionNumber")
	if errMaj == nil && errMin == nil {
		version = fmt.Sprintf("%d.%d.%s", major, minor, build)
	} else if cv, _, err := k.GetStringValue("CurrentVersion"); err == nil && cv != "" {
		version = cv + "." + build
	} else {
		version = build
	}
	if ubr, _, err := k.GetIntegerValue("UBR"); err == nil {
		version += fmt.Sprintf(".%d", ubr)
	}
	return version
}

// osMeta ajoute l'édition de Windows (ProductType : « 1 » poste de travail, « 2 »
// contrôleur de domaine, « 3 » serveur), qui décide de la règle des services automatiques.
func osMeta(meta map[string]string) {
	meta["windows_product_type"] = strconv.Itoa(int(windows.RtlGetVersion().ProductType))
}
