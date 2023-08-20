package slow

import (
	"encoding/binary"
	"fmt"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/protolambda/asterisc/rvgo/oracle"
)

const (
	fdStdin         = 0
	fdStdout        = 1
	fdStderr        = 2
	fdHintRead      = 3
	fdHintWrite     = 4
	fdPreimageRead  = 5
	fdPreimageWrite = 6
)

func decodeU64(v []byte) (out U64) {
	if len(v) > 8 {
		panic("bad u64 decode")
	}
	var x [8]byte // pad to 8 bytes
	copy(x[:], v)
	(*U256)(&out).SetUint64(binary.LittleEndian.Uint64(x[:]) & ((1 << (len(v) * 8)) - 1)) // mask out the lower bytes to get the size of uint we want
	return
}

func encodeU64(v U64, dest []byte) {
	if len(dest) != 8 {
		panic("bad u64 encode")
	}
	binary.LittleEndian.PutUint64(dest, v.val())
}

const (
	stateSizeMemRoot         = 32
	stateSizePreimageKey     = 32
	stateSizePreimageOffset  = 8
	stateSizePC              = 8
	stateSizeExitCode        = 1
	stateSizeExited          = 1
	stateSizeStep            = 8
	stateSizeHeap            = 8
	stateSizeLoadReservation = 8
	stateSizeRegisters       = 8 * 32
)

const (
	stateOffsetMemRoot         = 0
	stateOffsetPreimageKey     = stateOffsetMemRoot + stateSizeMemRoot
	stateOffsetPreimageOffset  = stateOffsetPreimageKey + stateSizePreimageKey
	stateOffsetPC              = stateOffsetPreimageOffset + stateSizePreimageOffset
	stateOffsetExitCode        = stateOffsetPC + stateSizePC
	stateOffsetExited          = stateOffsetExitCode + stateSizeExitCode
	stateOffsetStep            = stateOffsetExited + stateSizeExited
	stateOffsetHeap            = stateOffsetStep + stateSizeStep
	stateOffsetLoadReservation = stateOffsetHeap + stateSizeHeap
	stateOffsetRegisters       = stateOffsetLoadReservation + stateSizeLoadReservation
	stateSize                  = stateOffsetRegisters + stateSizeRegisters
)

func Step(calldata []byte, po oracle.PreImageOracle) (stateHash [32]byte, outErr error) {
	calldataload := func(offset U64) (out [32]byte) {
		copy(out[:], calldata[offset.val():])
		return
	}

	stateData := calldata[4 : 4+stateSize]

	computeStateHash := func() [32]byte {
		return crypto.Keccak256Hash(stateData)
	}

	getExited := func() (exited bool) {
		return stateData[stateOffsetExited] != 0
	}
	setExited := func() {
		stateData[stateOffsetExited] = 1
	}
	setExitCode := func(v uint8) {
		stateData[stateOffsetExitCode] = v
	}

	if getExited() {
		return computeStateHash(), nil
	}

	var revertCode uint64
	defer func() {
		if err := recover(); err != nil {
			outErr = fmt.Errorf("revert: %v", err)
		}
		if revertCode != 0 {
			outErr = fmt.Errorf("revert %x: %w", revertCode, outErr)
		}
	}()

	revertWithCode := func(code uint64, err error) {
		revertCode = code
		panic(err)
	}

	getMemRoot := func() (out [32]byte) {
		copy(out[:], stateData[stateOffsetMemRoot:stateOffsetMemRoot+32])
		return
	}
	setMemRoot := func(v [32]byte) {
		copy(stateData[stateOffsetMemRoot:stateOffsetMemRoot+32], v[:])
	}

	proofOffset := func(proofIndex uint8) (offset U64) {
		// proof size: 63 siblings, 1 leaf value, each 32 bytes
		offset = mul64(mul64(toU64(proofIndex), toU64(64)), toU64(32))
		offset = add64(offset, shortToU64(4+stateSize))
		return
	}

	hashPair := func(a [32]byte, b [32]byte) (h [32]byte) {
		return crypto.Keccak256Hash(a[:], b[:])
	}

	getMemoryB32 := func(addr U64, proofIndex uint8) (out [32]byte) {
		if and64(addr, toU64(31)) != (U64{}) { // quick addr alignment check
			revertWithCode(0xbad10ad0, fmt.Errorf("addr %d not aligned with 32 bytes", addr))
		}
		offset := proofOffset(proofIndex)
		leaf := calldataload(offset)
		offset = add64(offset, toU64(32))

		path := shr64(toU64(5), addr) // 32 bytes of memory per leaf
		node := leaf                  // starting from the leaf node, work back up by combining with siblings, to reconstruct the root
		for i := uint8(0); i < 64-5; i++ {
			sibling := calldataload(offset)
			offset = add64(offset, toU64(32))
			switch and64(shr64(toU64(i), path), toU64(1)).val() {
			case 0:
				node = hashPair(node, sibling)
			case 1:
				node = hashPair(sibling, node)
			}
		}
		memRoot := getMemRoot()
		if iszero(eq(b32asBEWord(node), b32asBEWord(memRoot))) { // verify the root matches
			revertWithCode(0x0badf00d, fmt.Errorf("reconstructed mem root: %x, expected %x", node, memRoot))
		}
		out = leaf
		return
	}

	// warning: setMemoryB32 does not verify the proof,
	// it assumes the same memory proof has been verified with getMemoryB32
	setMemoryB32 := func(addr U64, v [32]byte, proofIndex uint8) {
		if and64(addr, toU64(31)) != (U64{}) {
			revertWithCode(0xbad10ad0, fmt.Errorf("addr %d not aligned with 32 bytes", addr))
		}
		offset := proofOffset(proofIndex)
		leaf := v
		offset = add64(offset, toU64(32))
		path := shr64(toU64(5), addr) // 32 bytes of memory per leaf
		node := leaf                  // starting from the leaf node, work back up by combining with siblings, to reconstruct the root
		for i := uint8(0); i < 64-5; i++ {
			sibling := calldataload(offset)
			offset = add64(offset, toU64(32))

			switch and64(shr64(toU64(i), path), toU64(1)).val() {
			case 0:
				node = hashPair(node, sibling)
			case 1:
				node = hashPair(sibling, node)
			}
		}
		setMemRoot(node) // store new memRoot
	}

	// load unaligned, optionally signed, little-endian, integer of 1 ... 8 bytes from memory
	loadMem := func(addr U64, size U64, signed bool, proofIndexL uint8, proofIndexR uint8) (out U64) {
		if size.val() > 8 {
			panic(fmt.Errorf("cannot load more than 8 bytes: %d", size))
		}
		// load/verify left part
		leftAddr := and64(addr, not64(toU64(31)))
		left := b32asBEWord(getMemoryB32(leftAddr, proofIndexL))
		alignment := sub64(addr, leftAddr)

		right := U256{}
		rightAddr := and64(add64(addr, sub64(size, toU64(1))), not64(toU64(31)))
		leftShamt := sub64(sub64(toU64(32), alignment), size)
		rightShamt := toU64(0)
		if iszero64(eq64(leftAddr, rightAddr)) {
			// if unaligned, use second proof for the right part
			if proofIndexR == 0xff {
				panic("unexpected need for right-side proof in loadMem")
			}
			// load/verify right part
			right = b32asBEWord(getMemoryB32(rightAddr, proofIndexR))
			// left content is aligned to right of 32 bytes
			leftShamt = toU64(0)
			rightShamt = sub64(sub64(toU64(64), alignment), size)
		}

		// left: prepare for byte-taking by right-aligning
		left = shr(u64ToU256(shl64(toU64(3), leftShamt)), left)
		// right: right-align for byte-taking by right-aligning
		right = shr(u64ToU256(shl64(toU64(3), rightShamt)), right)
		// loop:
		for i := uint8(0); i < uint8(size.val()); i++ {
			// translate to reverse byte lookup, since we are reading little-endian memory, and need the highest byte first.
			// effAddr := (addr + size - 1 - i) &^ 31
			effAddr := and64(sub64(sub64(add64(addr, size), toU64(1)), toU64(i)), not64(toU64(31)))
			// take a byte from either left or right, depending on the effective address
			b := toU256(0)
			if eq64(effAddr, leftAddr) != (U64{}) {
				b = and(left, toU256(0xff))
				left = shr(toU256(8), left)
			}
			if eq64(effAddr, rightAddr) != (U64{}) {
				b = and(right, toU256(0xff))
				right = shr(toU256(8), right)
			}
			// append it to the output
			out = or64(shl64(toU64(8), out), u256ToU64(b))
		}

		if signed {
			signBitShift := sub64(shl64(toU64(3), size), toU64(1))
			out = signExtend64(out, signBitShift)
		}
		return
	}

	storeMemUnaligned := func(addr U64, size U64, value U256, proofIndexL uint8, proofIndexR uint8) {
		if size.val() > 32 {
			panic(fmt.Errorf("cannot store more than 32 bytes: %d", size))
		}

		leftAddr := and64(addr, not64(toU64(31)))
		rightAddr := and64(add64(addr, sub64(size, toU64(1))), not64(toU64(31)))
		alignment := sub64(addr, leftAddr)
		leftPatch := toU256(0)
		rightPatch := toU256(0)
		leftMask := toU256(0)
		rightMask := toU256(0)
		shift8 := toU256(8)
		min := alignment
		max := add64(alignment, size)
		for i := uint8(0); i < 64; i++ {
			index := toU64(i)
			leftSide := lt64(index, toU64(32)) != (U64{})
			if leftSide {
				leftPatch = shl(shift8, leftPatch)
				leftMask = shl(shift8, leftMask)
			} else {
				rightPatch = shl(shift8, rightPatch)
				rightMask = shl(shift8, rightMask)
			}
			if and64(eq64(lt64(index, min), toU64(0)), lt64(index, max)) != (U64{}) { // if alignment <= i < alignment+size
				b := and(shr(u64ToU256(shr64(toU64(3), sub64(index, alignment))), value), toU256(0xff))
				if leftSide {
					leftPatch = or(leftPatch, b)
					leftMask = or(leftMask, toU256(0xff))
				} else {
					rightPatch = or(rightPatch, b)
					rightMask = or(rightMask, toU256(0xff))
				}
			}
		}

		// load the left base
		left := b32asBEWord(getMemoryB32(leftAddr, proofIndexL))
		// apply the left patch
		left = or(and(left, not(leftMask)), leftPatch)
		// write the left
		setMemoryB32(leftAddr, beWordAsB32(left), proofIndexL)

		// if aligned: nothing more to do here
		if eq64(leftAddr, rightAddr) != (U64{}) {
			return
		}
		if proofIndexR == 0xff {
			panic("unexpected need for right-side proof in storeMem")
		}
		// load the right base (with updated mem root)
		right := b32asBEWord(getMemoryB32(rightAddr, proofIndexR))
		// apply the right patch
		right = or(and(right, not(rightMask)), rightPatch)
		// write the right (with updated mem root)
		setMemoryB32(rightAddr, beWordAsB32(right), proofIndexR)
	}
	storeMem := func(addr U64, size U64, value U64, proofIndexL uint8, proofIndexR uint8) {
		storeMemUnaligned(addr, size, u64ToU256(value), proofIndexL, proofIndexR)
	}

	loadRegister := func(reg U64) U64 {
		if gt64(reg, toU64(31)) != (U64{}) {
			revertWithCode(0xbad4e9, fmt.Errorf("cannot load invalid register: %d", reg.val()))
		}
		//fmt.Printf("load reg %2d: %016x\n", reg, state.Registers[reg])
		offset := add64(toU64(stateOffsetRegisters), mul64(reg, toU64(8)))
		return decodeU64(stateData[offset.val() : offset.val()+8])
	}
	writeRegister := func(reg U64, v U64) {
		//fmt.Printf("write reg %2d: %016x   value: %016x\n", reg, state.Registers[reg], v)
		if iszero64(reg) { // reg 0 must stay 0
			// v is a HINT, but no hints are specified by standard spec, or used by us.
			return
		}
		if gt64(reg, toU64(31)) != (U64{}) {
			revertWithCode(0xbad4e9, fmt.Errorf("unknown register %d, cannot write %x", reg.val(), v.val()))
		}
		offset := add64(toU64(stateOffsetRegisters), mul64(reg, toU64(8)))
		encodeU64(v, stateData[offset.val():offset.val()+8])
	}

	getLoadReservation := func() U64 {
		return decodeU64(stateData[stateOffsetLoadReservation : stateOffsetLoadReservation+8])
	}
	setLoadReservation := func(addr U64) {
		encodeU64(addr, stateData[stateOffsetLoadReservation:stateOffsetLoadReservation+8])
	}

	writeCSR := func(num U64, v U64) {
		// TODO: do we need CSR?
	}

	readCSR := func(num U64) U64 {
		// TODO: do we need CSR?
		return toU64(0)
	}

	getPC := func() U64 {
		return decodeU64(stateData[stateOffsetPC : stateOffsetPC+8])
	}
	setPC := func(pc U64) {
		encodeU64(pc, stateData[stateOffsetPC:stateOffsetPC+8])
	}

	getHeap := func() U64 {
		return decodeU64(stateData[stateOffsetHeap : stateOffsetHeap+8])
	}
	setHeap := func(v U64) {
		encodeU64(v, stateData[stateOffsetHeap:stateOffsetHeap+8])
	}

	getPreimageKey := func() [32]byte {
		return *(*[32]byte)(stateData[stateOffsetPreimageKey : stateOffsetPreimageKey+32])
	}
	setPreimageKey := func(k [32]byte) {
		copy(stateData[stateOffsetPreimageKey:stateOffsetPreimageKey+32], k[:])
	}

	getPreimageOffset := func() U64 {
		return decodeU64(stateData[stateOffsetPreimageOffset : stateOffsetPreimageOffset+8])
	}
	setPreimageOffset := func(v U64) {
		encodeU64(v, stateData[stateOffsetPreimageOffset:stateOffsetPreimageOffset+8])
	}

	updateCSR := func(num U64, v U64, mode U64) (out U64) {
		out = readCSR(num)
		switch mode.val() {
		case 1: // ?01 = CSRRW(I)
		case 2: // ?10 = CSRRS(I)
			v = or64(out, v)
		case 3: // ?11 = CSRRC(I)
			v = and64(out, not64(v))
		default:
			revertWithCode(0xbadc0de0, fmt.Errorf("unkwown CSR mode: %d", mode.val()))
		}
		writeCSR(num, v)
		return
	}

	writePreimageKey := func(addr U64, count U64) U64 {
		// adjust count down, so we only have to read a single 32 byte leaf of memory
		alignment := and64(addr, toU64(31))
		maxData := sub64(toU64(32), alignment)
		if gt64(count, maxData) != (U64{}) {
			count = maxData
		}

		dat := b32asBEWord(getMemoryB32(sub64(addr, alignment), 1))
		// shift out leading bits
		dat = shl(u64ToU256(shl64(toU64(3), alignment)), dat)
		// shift to right end, remove trailing bits
		dat = shr(u64ToU256(shl64(toU64(3), sub64(toU64(32), count))), dat)

		bits := shl(toU256(3), u64ToU256(count))

		preImageKey := getPreimageKey()

		// Append to key content by bit-shifting
		key := b32asBEWord(preImageKey)
		key = shl(bits, key)
		key = or(key, dat)

		// We reset the pre-image value offset back to 0 (the right part of the merkle pair)
		setPreimageKey(beWordAsB32(key))
		setPreimageOffset(toU64(0))
		return count
	}

	readPreimageValue := func(addr U64, count U64) U64 {
		preImageKey, offset := getPreimageKey(), getPreimageOffset()

		pdatB32, pdatlen, err := po.ReadPreImagePart(preImageKey, offset.val()) // pdat is left-aligned
		if err != nil {
			revertWithCode(0xbadf00d, err)
		}
		if iszero64(toU64(pdatlen)) { // EOF
			return toU64(0)
		}
		alignment := and64(addr, toU64(31))    // how many bytes addr is offset from being left-aligned
		maxData := sub64(toU64(32), alignment) // higher alignment leaves less room for data this step
		if gt64(count, maxData) != (U64{}) {
			count = maxData
		}
		if gt64(count, toU64(pdatlen)) != (U64{}) { // cannot read more than pdatlen
			count = toU64(pdatlen)
		}

		bits := shl64(toU64(3), sub64(toU64(32), count))             // 32-count, in bits
		mask := not(sub(shl(u64ToU256(bits), toU256(1)), toU256(1))) // left-aligned mask for count bytes
		alignmentBits := u64ToU256(shl64(toU64(3), alignment))
		mask = shr(alignmentBits, mask)                  // mask of count bytes, shifted by alignment
		pdat := shr(alignmentBits, b32asBEWord(pdatB32)) // pdat, shifted by alignment

		// update pre-image reader with updated offset
		newOffset := add64(offset, count)
		setPreimageOffset(newOffset)

		node := getMemoryB32(sub64(addr, alignment), 1)
		dat := and(b32asBEWord(node), not(mask)) // keep old bytes outside of mask
		dat = or(dat, and(pdat, mask))           // fill with bytes from pdat
		setMemoryB32(sub64(addr, alignment), beWordAsB32(dat), 1)
		return count
	}

	sysCall := func() {
		a7 := loadRegister(toU64(17))
		switch a7.val() {
		case 93: // exit the calling thread. No multi-thread support yet, so just exit.
			a0 := loadRegister(toU64(10))
			setExitCode(uint8(a0.val()))
			setExited()
			// program stops here, no need to change registers.
		case 94: // exit-group
			a0 := loadRegister(toU64(10))
			setExitCode(uint8(a0.val()))
			setExited()
		case 214: // brk
			// Go sys_linux_riscv64 runtime will only ever call brk(NULL), i.e. first argument (register a0) set to 0.

			// brk(0) changes nothing about the memory, and returns the current page break
			v := shl64(toU64(30), toU64(1)) // set program break at 1 GiB
			writeRegister(toU64(10), v)
			writeRegister(toU64(11), toU64(0)) // no error
		case 222: // mmap
			// A0 = addr (hint)
			addr := loadRegister(toU64(10))
			// A1 = n (length)
			length := loadRegister(toU64(11))
			// A2 = prot (memory protection type, can ignore)
			// A3 = flags (shared with other process and or written back to file, can ignore)  // TODO maybe assert the MAP_ANONYMOUS flag is set
			// A4 = fd (file descriptor, can ignore because we support anon memory only)
			// A5 = offset (offset in file, we don't support any non-anon memory, so we can ignore this)

			// ignore: prot, flags, fd, offset
			switch addr.val() {
			case 0:
				// No hint, allocate it ourselves, by as much as the requested length.
				// Increase the length to align it with desired page size if necessary.
				align := and64(length, shortToU64(4095))
				if align != (U64{}) {
					length = add64(length, sub64(shortToU64(4096), align))
				}
				prevHeap := getHeap()
				writeRegister(toU64(10), prevHeap)
				setHeap(add64(prevHeap, length)) // increment heap with length
				//fmt.Printf("mmap: 0x%016x (+ 0x%x increase)\n", s.Heap, length)
			default:
				// allow hinted memory address (leave it in A0 as return argument)
				//fmt.Printf("mmap: 0x%016x (0x%x allowed)\n", addr, length)
			}
			writeRegister(toU64(11), toU64(0)) // no error
		case 63: // read
			fd := loadRegister(toU64(10))    // A0 = fd
			addr := loadRegister(toU64(11))  // A1 = *buf addr
			count := loadRegister(toU64(12)) // A2 = count
			var n, errCode U64
			switch fd.val() {
			case fdStdin: // stdin
				n = toU64(0) // never read anything from stdin
				errCode = toU64(0)
			case fdHintRead: // hint-read
				// say we read it all, to continue execution after reading the hint-write ack response
				n = count
				errCode = toU64(0)
			case fdPreimageRead:
				n = readPreimageValue(addr, count)
				errCode = toU64(0)
			default:
				n = u64Mask()         //  -1 (reading error)
				errCode = toU64(0x4d) // EBADF
			}
			writeRegister(toU64(10), n)
			writeRegister(toU64(11), errCode)
		case 64: // write
			fd := loadRegister(toU64(10))    // A0 = fd
			addr := loadRegister(toU64(11))  // A1 = *buf addr
			count := loadRegister(toU64(12)) // A2 = count
			var n, errCode U64
			switch fd.val() {
			case fdStdout: // stdout
				n = count // write completes fully in single instruction step
				errCode = toU64(0)
			case fdStderr: // stderr
				n = count // write completes fully in single instruction step
				errCode = toU64(0)
			case fdHintWrite: // hint-write
				n = count
				errCode = toU64(0)
			case fdPreimageWrite: // pre-image key write
				n = writePreimageKey(addr, count)
				errCode = toU64(0) // no error
			default: // any other file, including (3) hint read (5) preimage read
				n = u64Mask()         //  -1 (writing error)
				errCode = toU64(0x4d) // EBADF
			}
			writeRegister(toU64(10), n)
			writeRegister(toU64(11), errCode)
		case 25: // fcntl - file descriptor manipulation / info lookup
			fd := loadRegister(toU64(10))  // A0 = fd
			cmd := loadRegister(toU64(11)) // A1 = cmd
			var out, errCode U64
			switch cmd.val() {
			case 0x3: // F_GETFL: get file descriptor flags
				switch fd.val() {
				case 0: // stdin
					out = toU64(0) // O_RDONLY
				case 1: // stdout
					out = toU64(1) // O_WRONLY
				case 2: // stderr
					out = toU64(1) // O_WRONLY
				case 3: // hint-read
					out = toU64(0) // O_RDONLY
				case 4: // hint-write
					out = toU64(1) // O_WRONLY
				case 5: // pre-image read
					out = toU64(0) // O_RDONLY
				case 6: // pre-image write
					out = toU64(1) // O_WRONLY
				default:
					out = u64Mask()
					errCode = toU64(0x4d) // EBADF
				}
			default: // no other commands: don't allow changing flags, duplicating FDs, etc.
				out = u64Mask()
				errCode = toU64(0x16) // EINVAL (cmd not recognized by this kernel)
			}
			writeRegister(toU64(10), out)
			writeRegister(toU64(11), errCode) // EBADF
		case 56: // openat - the Go linux runtime will try to open optional /sys/kernel files for performance hints
			writeRegister(toU64(10), u64Mask())
			writeRegister(toU64(11), toU64(0xd)) // EACCES - no access allowed
		case 123: // sched_getaffinity - hardcode to indicate affinity with any cpu-set mask
			writeRegister(toU64(10), toU64(0))
			writeRegister(toU64(11), toU64(0))
		case 113: // clock_gettime
			addr := loadRegister(toU64(11)) // addr of timespec struct
			// first 8 bytes: tv_sec: 1337 seconds
			// second 8 bytes: tv_nsec: 1337*1000000000 nanoseconds (must be nonzero to pass Go runtimeInitTime check)
			storeMemUnaligned(addr, toU64(16), or(u64ToU256(shortToU64(1337)), shl(toU256(64), longToU256(1_337_000_000_000))), 1, 2)
			writeRegister(toU64(10), toU64(0))
			writeRegister(toU64(11), toU64(0))
		case 135: // rt_sigprocmask - ignore any sigset changes
			writeRegister(toU64(10), toU64(0))
			writeRegister(toU64(11), toU64(0))
		case 132: // sigaltstack - ignore any hints of an alternative signal receiving stack addr
			writeRegister(toU64(10), toU64(0))
			writeRegister(toU64(11), toU64(0))
		case 178: // gettid - hardcode to 0
			writeRegister(toU64(10), toU64(0))
			writeRegister(toU64(11), toU64(0))
		case 134: // rt_sigaction - no-op, we never send signals, and thus need no sig handler info
			writeRegister(toU64(10), toU64(0))
			writeRegister(toU64(11), toU64(0))
		//case 220: // clone - not supported
		case 163: // getrlimit
			res := loadRegister(toU64(10))
			addr := loadRegister(toU64(11))
			switch res.val() {
			case 0x7: // RLIMIT_NOFILE
				// first 8 bytes: soft limit. 1024 file handles max open
				// second 8 bytes: hard limit
				storeMemUnaligned(addr, toU64(16), or(shortToU256(1024), shl(toU256(64), shortToU256(1024))), 1, 2)
			default:
				revertWithCode(0xf0012, fmt.Errorf("unrecognized resource limit lookup: %d", res))
			}
		default:
			revertWithCode(0xf001ca11, fmt.Errorf("unrecognized system call: %d", a7))
		}
	}

	pc := getPC()
	instr := loadMem(pc, toU64(4), false, 0, 0xff) // raw instruction

	// these fields are ignored if not applicable to the instruction type / opcode
	opcode := parseOpcode(instr)
	rd := parseRd(instr) // destination register index
	funct3 := parseFunct3(instr)
	rs1 := parseRs1(instr) // source register 1 index
	rs2 := parseRs2(instr) // source register 2 index
	funct7 := parseFunct7(instr)

	switch opcode.val() {
	case 0x03: // 000_0011: memory loading
		// LB, LH, LW, LD, LBU, LHU, LWU
		imm := parseImmTypeI(instr)
		signed := iszero64(and64(funct3, toU64(4)))      // 4 = 100 -> bitflag
		size := shl64(and64(funct3, toU64(3)), toU64(1)) // 3 = 11 -> 1, 2, 4, 8 bytes size
		rs1Value := loadRegister(rs1)
		memIndex := add64(rs1Value, signExtend64(imm, toU64(11)))
		rdValue := loadMem(memIndex, size, signed, 1, 2)
		writeRegister(rd, rdValue)
		setPC(add64(pc, toU64(4)))
	case 0x23: // 010_0011: memory storing
		// SB, SH, SW, SD
		imm := parseImmTypeS(instr)
		size := shl64(funct3, toU64(1))
		value := loadRegister(rs2)
		rs1Value := loadRegister(rs1)
		memIndex := add64(rs1Value, signExtend64(imm, toU64(11)))
		storeMem(memIndex, size, value, 1, 2)
		setPC(add64(pc, toU64(4)))
	case 0x63: // 110_0011: branching
		rs1Value := loadRegister(rs1)
		rs2Value := loadRegister(rs2)
		branchHit := toU64(0)
		switch funct3.val() {
		case 0: // 000 = BEQ
			branchHit = eq64(rs1Value, rs2Value)
		case 1: // 001 = BNE
			branchHit = and64(not64(eq64(rs1Value, rs2Value)), toU64(1))
		case 4: // 100 = BLT
			branchHit = slt64(rs1Value, rs2Value)
		case 5: // 101 = BGE
			branchHit = and64(not64(slt64(rs1Value, rs2Value)), toU64(1))
		case 6: // 110 = BLTU
			branchHit = lt64(rs1Value, rs2Value)
		case 7: // 111 = BGEU
			branchHit = and64(not64(lt64(rs1Value, rs2Value)), toU64(1))
		}
		switch branchHit.val() {
		case 0:
			pc = add64(pc, toU64(4))
		default:
			imm := parseImmTypeB(instr)
			// imm is a signed offset, in multiples of 2 bytes.
			// So it's really 13 bits with a hardcoded 0 bit.
			pc = add64(pc, imm)
		}
		// not like the other opcodes: nothing to write to rd register, and PC has already changed
		setPC(pc)
	case 0x13: // 001_0011: immediate arithmetic and logic
		rs1Value := loadRegister(rs1)
		imm := parseImmTypeI(instr)
		var rdValue U64
		switch funct3.val() {
		case 0: // 000 = ADDI
			rdValue = add64(rs1Value, imm)
		case 1: // 001 = SLLI
			rdValue = shl64(and64(imm, toU64(0x3F)), rs1Value) // lower 6 bits in 64 bit mode
		case 2: // 010 = SLTI
			rdValue = slt64(rs1Value, imm)
		case 3: // 011 = SLTIU
			rdValue = lt64(rs1Value, imm)
		case 4: // 100 = XORI
			rdValue = xor64(rs1Value, imm)
		case 5: // 101 = SR~
			switch shr64(toU64(6), imm).val() { // in rv64i the top 6 bits select the shift type
			case 0x00: // 000000 = SRLI
				rdValue = shr64(and64(imm, toU64(0x3F)), rs1Value) // lower 6 bits in 64 bit mode
			case 0x10: // 010000 = SRAI
				rdValue = sar64(and64(imm, toU64(0x3F)), rs1Value) // lower 6 bits in 64 bit mode
			}
		case 6: // 110 = ORI
			rdValue = or64(rs1Value, imm)
		case 7: // 111 = ANDI
			rdValue = and64(rs1Value, imm)
		}
		writeRegister(rd, rdValue)
		setPC(add64(pc, toU64(4)))
	case 0x1B: // 001_1011: immediate arithmetic and logic signed 32 bit
		rs1Value := loadRegister(rs1)
		imm := parseImmTypeI(instr)
		var rdValue U64
		switch funct3.val() {
		case 0: // 000 = ADDIW
			rdValue = mask32Signed64(add64(rs1Value, imm))
		case 1: // 001 = SLLIW
			rdValue = mask32Signed64(shl64(and64(imm, toU64(0x1F)), rs1Value))
		case 5: // 101 = SR~
			shamt := and64(imm, toU64(0x1F))
			switch shr64(toU64(6), imm).val() { // in rv64i the top 6 bits select the shift type
			case 0x00: // 000000 = SRLIW
				rdValue = signExtend64(shr64(shamt, and64(rs1Value, u32Mask())), toU64(31))
			case 0x10: // 010000 = SRAIW
				rdValue = signExtend64(shr64(shamt, and64(rs1Value, u32Mask())), sub64(toU64(31), shamt))
			}
		}
		writeRegister(rd, rdValue)
		setPC(add64(pc, toU64(4)))
	case 0x33: // 011_0011: register arithmetic and logic
		rs1Value := loadRegister(rs1)
		rs2Value := loadRegister(rs2)
		var rdValue U64
		switch funct7.val() {
		case 1: // RV M extension
			switch funct3.val() {
			case 0: // 000 = MUL: signed x signed
				rdValue = mul64(rs1Value, rs2Value)
			case 1: // 001 = MULH: upper bits of signed x signed
				rdValue = u256ToU64(shr(toU256(64), mul(signExtend64To256(rs1Value), signExtend64To256(rs2Value))))
			case 2: // 010 = MULHSU: upper bits of signed x unsigned
				rdValue = u256ToU64(shr(toU256(64), mul(signExtend64To256(rs1Value), u64ToU256(rs2Value))))
			case 3: // 011 = MULHU: upper bits of unsigned x unsigned
				rdValue = u256ToU64(shr(toU256(64), mul(u64ToU256(rs1Value), u64ToU256(rs2Value))))
			case 4: // 100 = DIV
				switch rs2Value.val() {
				case 0:
					rdValue = u64Mask()
				default:
					rdValue = sdiv64(rs1Value, rs2Value)
				}
			case 5: // 101 = DIVU
				switch rs2Value.val() {
				case 0:
					rdValue = u64Mask()
				default:
					rdValue = div64(rs1Value, rs2Value)
				}
			case 6: // 110 = REM
				switch rs2Value.val() {
				case 0:
					rdValue = rs1Value
				default:
					rdValue = smod64(rs1Value, rs2Value)
				}
			case 7: // 111 = REMU
				switch rs2Value.val() {
				case 0:
					rdValue = rs1Value
				default:
					rdValue = mod64(rs1Value, rs2Value)
				}
			}
		default:
			switch funct3.val() {
			case 0: // 000 = ADD/SUB
				switch funct7.val() {
				case 0x00: // 0000000 = ADD
					rdValue = add64(rs1Value, rs2Value)
				case 0x20: // 0100000 = SUB
					rdValue = sub64(rs1Value, rs2Value)
				}
			case 1: // 001 = SLL
				rdValue = shl64(and64(rs2Value, toU64(0x3F)), rs1Value) // only the low 6 bits are consider in RV6VI
			case 2: // 010 = SLT
				rdValue = slt64(rs1Value, rs2Value)
			case 3: // 011 = SLTU
				rdValue = lt64(rs1Value, rs2Value)
			case 4: // 100 = XOR
				rdValue = xor64(rs1Value, rs2Value)
			case 5: // 101 = SR~
				switch funct7.val() {
				case 0x00: // 0000000 = SRL
					rdValue = shr64(and64(rs2Value, toU64(0x3F)), rs1Value) // logical: fill with zeroes
				case 0x20: // 0100000 = SRA
					rdValue = sar64(and64(rs2Value, toU64(0x3F)), rs1Value) // arithmetic: sign bit is extended
				}
			case 6: // 110 = OR
				rdValue = or64(rs1Value, rs2Value)
			case 7: // 111 = AND
				rdValue = and64(rs1Value, rs2Value)
			}
		}
		writeRegister(rd, rdValue)
		setPC(add64(pc, toU64(4)))
	case 0x3B: // 011_1011: register arithmetic and logic in 32 bits
		rs1Value := loadRegister(rs1)
		rs2Value := loadRegister(rs2)
		var rdValue U64
		switch funct7.val() {
		case 1: // RV M extension
			switch funct3.val() {
			case 0: // 000 = MULW
				rdValue = mask32Signed64(mul64(and64(rs1Value, u32Mask()), and64(rs2Value, u32Mask())))
			case 4: // 100 = DIVW
				switch rs2Value.val() {
				case 0:
					rdValue = u64Mask()
				default:
					rdValue = mask32Signed64(sdiv64(mask32Signed64(rs1Value), mask32Signed64(rs2Value)))
				}
			case 5: // 101 = DIVUW
				switch rs2Value.val() {
				case 0:
					rdValue = u64Mask()
				default:
					rdValue = mask32Signed64(div64(and64(rs1Value, u32Mask()), and64(rs2Value, u32Mask())))
				}
			case 6: // 110 = REMW
				switch rs2Value.val() {
				case 0:
					rdValue = mask32Signed64(rs1Value)
				default:
					rdValue = mask32Signed64(smod64(mask32Signed64(rs1Value), mask32Signed64(rs2Value)))
				}
			case 7: // 111 = REMUW
				switch rs2Value.val() {
				case 0:
					rdValue = mask32Signed64(rs1Value)
				default:
					rdValue = mask32Signed64(mod64(and64(rs1Value, u32Mask()), and64(rs2Value, u32Mask())))
				}
			}
		default:
			switch funct3.val() {
			case 0: // 000 = ADDW/SUBW
				switch funct7.val() {
				case 0x00: // 0000000 = ADDW
					rdValue = mask32Signed64(add64(and64(rs1Value, u32Mask()), and64(rs2Value, u32Mask())))
				case 0x20: // 0100000 = SUBW
					rdValue = mask32Signed64(sub64(and64(rs1Value, u32Mask()), and64(rs2Value, u32Mask())))
				}
			case 1: // 001 = SLLW
				rdValue = mask32Signed64(shl64(and64(rs2Value, toU64(0x1F)), rs1Value))
			case 5: // 101 = SR~
				shamt := and64(rs2Value, toU64(0x1F))
				switch funct7.val() {
				case 0x00: // 0000000 = SRLW
					rdValue = signExtend64(shr64(shamt, and64(rs1Value, u32Mask())), toU64(31))
				case 0x20: // 0100000 = SRAW
					rdValue = signExtend64(shr64(shamt, and64(rs1Value, u32Mask())), sub64(toU64(31), shamt))
				}
			}
		}
		writeRegister(rd, rdValue)
		setPC(add64(pc, toU64(4)))
	case 0x37: // 011_0111: LUI = Load upper immediate
		imm := parseImmTypeU(instr)
		rdValue := shl64(toU64(12), imm)
		writeRegister(rd, rdValue)
		setPC(add64(pc, toU64(4)))
	case 0x17: // 001_0111: AUIPC = Add upper immediate to PC
		imm := parseImmTypeU(instr)
		rdValue := add64(pc, signExtend64(shl64(toU64(12), imm), toU64(31)))
		writeRegister(rd, rdValue)
		setPC(add64(pc, toU64(4)))
	case 0x6F: // 110_1111: JAL = Jump and link
		imm := parseImmTypeJ(instr)
		rdValue := add64(pc, toU64(4))
		writeRegister(rd, rdValue)
		setPC(add64(pc, signExtend64(shl64(toU64(1), imm), toU64(20)))) // signed offset in multiples of 2 bytes (last bit is there, but ignored)
	case 0x67: // 110_0111: JALR = Jump and link register
		rs1Value := loadRegister(rs1)
		imm := parseImmTypeI(instr)
		rdValue := add64(pc, toU64(4))
		writeRegister(rd, rdValue)
		setPC(and64(add64(rs1Value, signExtend64(imm, toU64(11))), xor64(u64Mask(), toU64(1)))) // least significant bit is set to 0
	case 0x73: // 111_0011: environment things
		switch funct3.val() {
		case 0: // 000 = ECALL/EBREAK
			switch shr64(toU64(20), instr).val() { // I-type, top 12 bits
			case 0: // imm12 = 000000000000 ECALL
				sysCall()
				setPC(add64(pc, toU64(4)))
			default: // imm12 = 000000000001 EBREAK
				setPC(add64(pc, toU64(4))) // ignore breakpoint
			}
		default: // CSR instructions
			imm := parseCSSR(instr)
			value := rs1
			if iszero64(and64(funct3, toU64(4))) {
				value = loadRegister(rs1)
			}
			mode := and64(funct3, toU64(3))
			rdValue := updateCSR(imm, value, mode)
			writeRegister(rd, rdValue)
			setPC(add64(pc, toU64(4)))
		}
	case 0x2F: // 010_1111: RV32A and RV32A atomic operations extension
		// acquire and release bits:
		//   aq := and64(shr64(toU64(1), funct7), toU64(1))
		//   rl := and64(funct7, toU64(1))
		// if none set: unordered
		// if aq is set: no following mem ops observed before acquire mem op
		// if rl is set: release mem op not observed before earlier mem ops
		// if both set: sequentially consistent
		// These are no-op here because there is no pipeline of mem ops to acquire/release.

		// 0b010 == RV32A W variants
		// 0b011 == RV64A D variants
		size := shl64(funct3, toU64(1))
		if lt64(size, toU64(4)) != (U64{}) {
			revertWithCode(0xbada70, fmt.Errorf("bad AMO size: %d", size))
		}
		addr := loadRegister(rs1)
		// TODO check if addr is aligned

		op := shr64(toU64(2), funct7)
		switch op.val() {
		case 0x2: // 00010 = LR = Load Reserved
			v := loadMem(addr, size, true, 1, 2)
			writeRegister(rd, v)
			setLoadReservation(addr)
		case 0x3: // 00011 = SC = Store Conditional
			rdValue := toU64(1)
			if eq64(addr, getLoadReservation()) != (U64{}) {
				rs2Value := loadRegister(rs2)
				storeMem(addr, size, rs2Value, 1, 2)
				rdValue = toU64(0)
			}
			writeRegister(rd, rdValue)
			setLoadReservation(toU64(0))
		default: // AMO: Atomic Memory Operation
			rs2Value := loadRegister(rs2)
			if eq64(size, toU64(4)) != (U64{}) {
				rs2Value = mask32Signed64(rs2Value)
			}
			value := rs2Value
			v := loadMem(addr, size, true, 1, 2)
			switch op.val() {
			case 0x0: // 00000 = AMOADD = add
				v = add64(v, value)
			case 0x1: // 00001 = AMOSWAP
				v = value
			case 0x4: // 00100 = AMOXOR = xor
				v = xor64(v, value)
			case 0x8: // 01000 = AMOOR = or
				v = or64(v, value)
			case 0xc: // 01100 = AMOAND = and
				v = and64(v, value)
			case 0x10: // 10000 = AMOMIN = min signed
				if slt64(value, v) != (U64{}) {
					v = value
				}
			case 0x14: // 10100 = AMOMAX = max signed
				if sgt64(value, v) != (U64{}) {
					v = value
				}
			case 0x18: // 11000 = AMOMINU = min unsigned
				if lt64(value, v) != (U64{}) {
					v = value
				}
			case 0x1c: // 11100 = AMOMAXU = max unsigned
				if gt64(value, v) != (U64{}) {
					v = value
				}
			default:
				revertWithCode(0xf001a70, fmt.Errorf("unknown atomic operation %d", op))
			}
			storeMem(addr, size, v, 1, 3) // after overwriting 1, proof 2 is no longer valid
			rdValue := v
			writeRegister(rd, rdValue)
		}
		setPC(add64(pc, toU64(4)))
	case 0x0F: // 000_1111: fence
		// Used to impose additional ordering constraints; flushing the mem operation pipeline.
		// This VM doesn't have a pipeline, nor additional harts, so this is a no-op.
		// FENCE / FENCE.TSO / FENCE.I all no-op: there's nothing to synchronize.
		setPC(add64(pc, toU64(4)))
	case 0x07: // FLW/FLD: floating point load word/double
		setPC(add64(pc, toU64(4))) // no-op this.
	case 0x27: // FSW/FSD: floating point store word/double
		setPC(add64(pc, toU64(4))) // no-op this.
	case 0x53: // FADD etc. no-op is enough to pass Go runtime check
		setPC(add64(pc, toU64(4))) // no-op this.
	default:
		revertWithCode(0xf001c0de, fmt.Errorf("unknown instruction opcode: %d", opcode))
	}
	return
}
