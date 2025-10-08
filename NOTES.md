This branch implements a proposal for the APIs that are described in
https://docs.google.com/document/d/1ml4lc4PDx_qkhU38PWaA-Qw-4rHV71oPhA_inFJJDLM/edit?tab=t.0#heading=h.9u0snvhf683d.

### Try it out

To run the service:
1. Get the salesforce credentials and put them in `./start.sh`
2. Create a github client with `redirect_uri=http://localhost:4433/self-service/methods/oidc/callback/github`
   and store the credentials in `.env`
3. Run `./start.sh`

Try to log in from http://127.0.0.1:4446/. You will be denied as the default behavior is deny

Create a group called `Identity`:
```console
curl -XPOST  http://localhost:8000/api/v0/groups -d '{"name": "Identity"}'
```

Get the client_id from the `./start.sh` run:
```console
export CLIENT_ID=<client_id>
```
And allow access to the group you created:
```console
curl -XPOST  http://localhost:8000/api/v0/groups/1/apps -d '{"client_id": "$CLIENT_ID"}'
```
NOTE: `1` is the group ID for the group we created before

Try to log in again, this time you should be able to finish login.

Now let's remove the app access:
```console
curl -XDELETE  http://localhost:8000/api/v0/groups/1/apps -d '{"client_id": "$CLIENT_ID"}'
```

Logging in now should fail.

Now get your user ID from Kratos:
```console
export EMAIL=<your-email>@canonical.com
export USER_ID=kratos list identities -e http://localhost:4434 --format yaml | yq '.[]  | select(.traits.email == "$EMAIL") | .id'
```

Create a local group, assign it to your user and allow it to access the app:
```console
curl -XPOST  http://localhost:8000/api/v0/groups -d '{"name": "aba"}'
curl -XPOST  http://localhost:8000/api/v0/groups/1/apps -d '{"client_id": "$CLIENT_ID"}'
curl -XPOST  http://localhost:8000/api/v0/groups/1/users -d '{"user_id": "$USER_ID$"}'
```
NOTE: `1` will be reused for the group id, because we deleted the previous group

Now you should be able to log in again and in the access token the group `aba` will be
added along to your salesforce groups.

### Notes

You can see the new API URLs defined in [pkg/authorization](pkg/authorization/handlers.go)
and [pkg/groups](pkg/groups/handlers.go).
An in-memory storage implementation is used for the PoC. Transforming it to a database
schema should be straight-forward.

Each group name is assigned a group ID and the group ID is used to represent the object in ofga.
We need the internal communication between the `authorization` and `hooks` services with the
`groups` service in order to retrieve the group ID. The group name + organization name should
be unique and we could use that in openfga instead (it would get rid of the dependency),
BUT then we would not be able to implement group or organization renaming.

Issues:
- We are facing the same issues as in the admin UI with deleting groups, where we have
  to delete all the tuples associated with the group.
- Retrieving the user_id for local groups can be cumbersome, especially if it is an OIDC
  user that hasn't logged in yet, perhaps we should allow users to be added to groups by email
  as well
- We may need to convert some APIs to support batch actions in order to allow admins to prepopulate
  the database (e.g. for group creation)
