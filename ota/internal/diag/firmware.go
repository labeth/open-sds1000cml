// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

// firmwareRoots are walked for the inventory; the stick's own mount and the
// vendor's log and media trees are skipped (the stick holds this archive).
var firmwareRoots = []string{"/usr/bin/siglent", "/etc", "/root", "/home", "/opt", "/usr/local"}

// firmwareSkip are the prefixes the walk leaves out.
var firmwareSkip = []string{"/usr/bin/siglent/usr/media", "/usr/bin/siglent/usr/log"}
