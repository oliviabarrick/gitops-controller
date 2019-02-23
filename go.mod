module github.com/justinbarrick/git-controller

replace github.com/justinbarrick/backup-controller => /home/justin/usr/src/github.com/justinbarrick/backup-controller

require (
	github.com/davecgh/go-spew v1.1.1
	github.com/justinbarrick/backup-controller v0.0.0-20190222144618-0c646e0fe0a4
	github.com/kubernetes-csi/external-snapshotter v1.0.1
	gopkg.in/src-d/go-git.v4 v4.10.0
	k8s.io/api v0.0.0-20181121191454-a61488babbd6
	k8s.io/apimachinery v0.0.0-20190211022232-e355a776c090
	sigs.k8s.io/controller-runtime v0.1.10
)
