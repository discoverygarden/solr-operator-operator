# Docker Image Workflows

Expecting this to be maintained and pulled in across a number of Docker image repos, using [`git subtree`](https://git-memo.readthedocs.io/en/latest/subtree.html).

Where used, should be able to:

Pull workflows, to update them locally:

```bash
git subtree pull --prefix=.github/workflows git@github.com:discoverygarden/docker-image-workflows.git
```

Push workflows, to update the remote (and subsequently pull them into the other image-building repos?):

```bash
git subtree push --prefix=.github/workflows git@github.com:discoverygarden/docker-image-workflows.git
```
