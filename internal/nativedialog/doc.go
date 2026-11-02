// Package nativedialog ouvre le sélecteur de fichiers du système.
//
// Un navigateur maison ne convient pas : personne ne s'y retrouve pour aller
// chercher un fichier, et les raccourcis, favoris et disques réseau du bureau y
// sont absents. On passe donc par le dialogue que la plateforme fournit.
//
//   - Windows : le dialogue COM du système (IFileOpenDialog), en Go pur, ce qui
//     préserve la compilation sans cgo et le binaire unique ;
//   - Linux : le dialogue de KDE (kdialog) quand il est là, sinon ceux de GTK
//     (zenity, qarma, matedialog) ;
//   - macOS : osascript.
//
// L'utilisateur peut toujours taper un chemin à la main : les champs restent
// modifiables, et c'est le recours quand aucun dialogue n'est disponible.
package nativedialog
