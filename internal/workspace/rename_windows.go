package workspace

import "golang.org/x/sys/windows"

// renameNoReplace renames a file or folder without replacing a destination
// that appeared after the caller checked it. MoveFileEx without
// MOVEFILE_REPLACE_EXISTING refuses an existing destination, and still renames
// a name to the same name in another case.
func renameNoReplace(oldPath string, newPath string) error {
	from, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, 0)
}
