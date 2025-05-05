package solr

/*
Add/edit user message struct.

See: https://solr.apache.org/guide/8_4/basic-authentication-plugin.html#add-a-user-or-edit-a-password
*/
type setUsersMessage struct {
	Users map[string]string `json:"set-user"`
}

/*
Delete user message struct.

See: https://solr.apache.org/guide/8_4/basic-authentication-plugin.html#delete-a-user
*/
type deleteUsersMessage struct {
	Users []string `json:"delete-user"`
}

/*
User/role assignment struct.

See: https://solr.apache.org/guide/8_4/rule-based-authorization-plugin.html#map-roles-to-users
*/
type setUserRoleMessage struct {
	RoleMap map[string][]string `json:"set-user-role"`
}
