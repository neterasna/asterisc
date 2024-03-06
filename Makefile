fuzz:
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallExit ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallBrk ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallMmap ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallFcntl ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallOpenat ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallClockGettime ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallClone ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallGetrlimit ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallNoop ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateRead ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateHintRead ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStatePreimageRead ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateWrite ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateHintWrite ./rvgo/test
	go test -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStatePreimageWrite ./rvgo/test

fuzz-mac:
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallExit ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallBrk ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallMmap ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallFcntl ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallOpenat ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallClockGettime ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallClone ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallGetrlimit ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateSyscallNoop ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateRead ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateHintRead ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStatePreimageRead ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateWrite ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStateHintWrite ./rvgo/test
	go test -ldflags=-extldflags=-Wl,-ld_classic -run NOTAREALTEST -v -fuzztime 10s -fuzz=FuzzStatePreimageWrite ./rvgo/test

.PHONY: \
  fuzz \
  fuzz-mac
