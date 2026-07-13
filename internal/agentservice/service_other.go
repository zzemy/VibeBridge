//go:build !windows

package agentservice

func Install(InstallOptions) error {
	return ErrUnsupported
}

func Uninstall() error {
	return ErrUnsupported
}

func QueryInstallation() (InstallationStatus, error) {
	return InstallationStatus{}, ErrUnsupported
}
