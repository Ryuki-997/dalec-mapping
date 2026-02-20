package contents

type BuildTarget string

const (
	AzLinux3Rpm           BuildTarget = "azlinux3/rpm"
	AzLinux3Container     BuildTarget = "azlinux3/container"
	NobleDeb              BuildTarget = "noble/deb"
	JammyDeb              BuildTarget = "jammy/deb"
	FocalDeb              BuildTarget = "focal/deb"
	BionicDeb             BuildTarget = "bionic/deb"
	BookwormDeb           BuildTarget = "bookworm/deb"
	WindowsCrossContainer BuildTarget = "windowscross/container"
)