# konfig-syncer

Sync `ConfigMap`s and `Secret`s between namespaces.

Builds can be at [Docker Hub](https://cloud.docker.com/repository/docker/n1koo/konfig-syncer/)
Docker image can be fetched with `docker pull n1koo/konfig-syncer`

## Usage

`konfig-syncer` uses `konfig-syncer` annotation for figuring out which objects should be synced and to which destionation `Namespace`s.

- `-kubeconfig` to point to kubeconfig
- `-master` to override master address in kubeconfig
- `-human-readable-logs`for disabling json logging output
- `-debug` flag to get more verbose logging

### Add
The value of the annotation (eg. `konfig-syncer: special-ns=true`) is used as label selector for finding the namespaces that have this label. Objects will be created/updated in to the matching namespaces.
If value of the annotation is empty (eg. `konfig-syncer: ""`) the obejct will be synced to *all* namespaces. 

If a new `Namespace` is created we check what objects should be inserted in to it.

### Update

The "origin" object is used as source of truth for updates. So if you change the data in this object the change will be propagated to all copied versions too.

If `Namespace`s labels get updated we sync what objects still belong to it (eg. create missing, delete the ones that are not required anymore)

### Delete

If the origin object is deleted the copied objects will also be deleted.

## Deployment

You can find example k8s and helm templates in the `deploy` dir

## TODO

- tests :(
- Fix name collisions (eg. figure out what should happen if theres two objects with same name in different NS trying to sync to third)
