package loader

// This file parses a fragment of C with libclang and stores the result for AST
// modification. It does not touch the AST itself.

import (
	"errors"
	"unsafe"
)

/*
#cgo CFLAGS: -I/usr/lib/llvm-7/include
#cgo LDFLAGS: -L/usr/lib/llvm-7/lib -lclang
#include <clang-c/Index.h> // if this fails, install libclang-7-dev
#include <stdlib.h>

int tinygo_clang_visitor_cgo(CXCursor c, CXCursor parent, CXClientData client_data);
*/
import "C"

var globalFileInfo *fileInfo

func (info *fileInfo) parseFragment(fragment string) error {
	index := C.clang_createIndex(0, 1)
	defer C.clang_disposeIndex(index)

	filenameC := C.CString("cgo-fake.c")
	defer C.free(unsafe.Pointer(filenameC))

	fragmentC := C.CString(fragment)
	defer C.free(unsafe.Pointer(fragmentC))

	unsavedFile := C.struct_CXUnsavedFile{
		Filename: filenameC,
		Length:   C.ulong(len(fragment)),
		Contents: fragmentC,
	}

	var unit C.CXTranslationUnit
	errCode := C.clang_parseTranslationUnit2(
		index,
		filenameC,
		(**C.char)(unsafe.Pointer(uintptr(0))), 0, // command line args
		&unsavedFile, 1, // unsaved files
		C.CXTranslationUnit_None,
		&unit)
	if errCode != 0 {
		panic("loader: failed to parse source with libclang")
	}
	defer C.clang_disposeTranslationUnit(unit)

	if C.clang_getNumDiagnostics(unit) != 0 {
		return errors.New("cgo: libclang cannot parse fragment")
	}

	if globalFileInfo != nil {
		// There is a race condition here but that doesn't really matter as it
		// is a sanity check anyway.
		panic("libclang.go cannot be used concurrently yet")
	}
	globalFileInfo = info
	defer func() {
		globalFileInfo = nil
	}()

	cursor := C.clang_getTranslationUnitCursor(unit)
	C.clang_visitChildren(cursor, (*[0]byte)((unsafe.Pointer(C.tinygo_clang_visitor_cgo))), C.CXClientData(uintptr(0)))

	return nil
}

//export tinygo_clang_visitor
func tinygo_clang_visitor(c, parent C.CXCursor, client_data C.CXClientData) C.int {
	info := globalFileInfo
	kind := C.clang_getCursorKind(c)
	switch kind {
	case C.CXCursor_FunctionDecl:
		name := getString(C.clang_getCursorSpelling(c))
		cursorType := C.clang_getCursorType(c)
		if C.clang_isFunctionTypeVariadic(cursorType) != 0 {
			return C.CXChildVisit_Continue // not supported
		}
		numArgs := C.clang_Cursor_getNumArguments(c)
		fn := &functionInfo{name: name}
		info.functions = append(info.functions, fn)
		for i := C.int(0); i < numArgs; i++ {
			arg := C.clang_Cursor_getArgument(c, C.uint(i))
			argName := getString(C.clang_getCursorSpelling(arg))
			argType := C.clang_getArgType(cursorType, C.uint(i))
			argTypeName := getString(C.clang_getTypeSpelling(argType))
			fn.args = append(fn.args, paramInfo{argName, argTypeName})
		}
		resultType := C.clang_getCursorResultType(c)
		resultTypeName := getString(C.clang_getTypeSpelling(resultType))
		fn.result = resultTypeName
	}
	return C.CXChildVisit_Continue
}

func getString(clangString C.CXString) (s string) {
	rawString := C.clang_getCString(clangString)
	s = C.GoString(rawString)
	C.clang_disposeString(clangString)
	return
}
