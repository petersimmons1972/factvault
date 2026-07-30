# investigative journalism

Fictional Plan 5 example domain for factvault. It defines properties, seed entities, a canned source fixture, and an expected dossier shape.

Set the required database and tenant configuration before loading the example:

```sh
export FACTVAULT_DATABASE_URL='postgres://app_user:dev_only_local_password@localhost:5432/factvault?sslmode=disable'
export FACTVAULT_DEV_TENANT_ID='11111111-1111-1111-1111-111111111111'
```

From the repository root:

```sh
factvault example load investigative-journalism --root ./examples
```

Or from this example directory:

```sh
cd examples/investigative-journalism
./run.sh
```
