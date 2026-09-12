package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type adapterBase struct {
	id         string
	candidates []string
	launcher   Launcher
}

func newAdapterBase(id string, candidates []string, launcher Launcher) adapterBase {
	if launcher == nil {
		launcher = newExecLauncher()
	}
	return adapterBase{id: id, candidates: append([]string(nil), candidates...), launcher: launcher}
}

func (a adapterBase) ID() string { return a.id }

func (a adapterBase) Detect(ctx context.Context) ([]Installation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	installations := make([]Installation, 0, len(a.candidates))
	for _, candidate := range a.candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			installations = append(installations, Installation{ID: a.id, Executable: candidate})
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect %s executable: %w", a.id, err)
		}
	}
	return installations, nil
}

func (a adapterBase) prepareProfileDirectory(ctx context.Context, session Session) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path := filepath.Join(session.ProfileRoot, session.Key, a.id)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("create %s profile: %w", a.id, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return "", fmt.Errorf("protect %s profile: %w", a.id, err)
	}
	return path, nil
}
