package httpapp

import "io/fs"

// fileOnlyFS prevents FileServer from publishing embedded directory listings.
type fileOnlyFS struct{ fs.FS }

func (files fileOnlyFS) Open(name string) (fs.File, error) {
	file, err := files.FS.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.IsDir() {
		file.Close()
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return file, nil
}
