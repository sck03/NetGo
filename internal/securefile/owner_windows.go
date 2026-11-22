package securefile

import "os"

func preserveOwner(_ *os.File, _ os.FileInfo) error { return nil }
