package herdr

import "time"

func setClock(s *Source, now func() time.Time) { s.now = now }

func setStateDir(s *Source, dir string) { s.stateDir = dir }
