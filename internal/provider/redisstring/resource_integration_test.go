//go:build integration

package redisstring_test

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/artuross/terraform-provider-redis/internal/acctest"
)

const stringAddress = "redis_string.test"

// a JSON config, the kind of value this resource is meant for
const configValue = `{"feature_flags": {"new_checkout": true}, "max_items": 50, "greeting": "Hello, world!"}`

func TestResource_Create(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			CheckDestroy:             checkKeys(server),
			Steps: []resource.TestStep{
				{
					Config: acctest.ProviderConfig(server.URL, stringResource("test", "app:config", configValue)),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(stringAddress, plancheck.ResourceActionCreate),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(stringAddress, "key", "app:config"),
						resource.TestCheckResourceAttr(stringAddress, "value", configValue),
						checkKeys(server, "app:config"),
						checkValue(server, "app:config", configValue),
					),
				},
			},
		})
	})

	t.Run("multiple strings", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			CheckDestroy:             checkKeys(server),
			Steps: []resource.TestStep{
				{
					Config: acctest.ProviderConfig(
						server.URL,
						stringResource("a", "a", "value a"),
						stringResource("b", "b", "value b"),
					),
					Check: resource.ComposeAggregateTestCheckFunc(
						checkKeys(server, "a", "b"),
						checkValue(server, "a", "value a"),
						checkValue(server, "b", "value b"),
					),
				},
			},
		})
	})

	t.Run("with credentials", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedisWithUsers(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(map[string]string{
				"REDIS_USERNAME": "admin",
				"REDIS_PASSWORD": "admin-password",
			}),
			CheckDestroy: checkKeys(server),
			Steps: []resource.TestStep{
				{
					Config: acctest.ProviderConfig(server.URL, stringResource("test", "app:config", configValue)),
					Check:  checkValue(server, "app:config", configValue),
				},
			},
		})
	})

	// called directly instead of through Terraform, to compare the diagnostics exactly
	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		t.Run("key already exists", func(t *testing.T) {
			t.Parallel()

			server := acctest.StartRedis(t)
			setKey(t, server, "app:config", "existing")

			resp := callCreate(t, directResource(t, server.URL, nil), "app:config", "new")

			want := diag.Diagnostics{diag.NewAttributeErrorDiagnostic(
				path.Root("key"),
				"key already exists",
				`The key "app:config" already exists. Import it to manage it with Terraform, or remove it first.`,
			)}
			assert.Equal(t, want, resp.Diagnostics)
			assert.True(t, resp.State.Raw.IsNull())

			// never overwritten
			require.NoError(t, checkValue(server, "app:config", "existing")(nil))
		})

		t.Run("key holds another type", func(t *testing.T) {
			t.Parallel()

			server := acctest.StartRedis(t)
			require.NoError(t, server.Client.HSet(context.Background(), "app:config", "field", "value").Err())

			resp := callCreate(t, directResource(t, server.URL, nil), "app:config", "new")

			require.Len(t, resp.Diagnostics, 1)
			assert.Equal(t, "key already exists", resp.Diagnostics[0].Summary())
			assert.Equal(t, "hash", keyType(t, server, "app:config"))
		})

		t.Run("no permission", func(t *testing.T) {
			t.Parallel()

			server := acctest.StartRedisWithUsers(t)
			env := map[string]string{"REDIS_USERNAME": "reader", "REDIS_PASSWORD": "reader-password"}

			resp := callCreate(t, directResource(t, server.URL, env), "app:config", configValue)

			acctest.AssertRedisError(t, resp.Diagnostics, "setting string")
			require.NoError(t, checkKeys(server)(nil))
		})

		t.Run("unreachable", func(t *testing.T) {
			t.Parallel()

			// nothing listens on port 1
			resp := callCreate(t, directResource(t, "redis://127.0.0.1:1", nil), "app:config", configValue)

			acctest.AssertRedisError(t, resp.Diagnostics, "setting string")
		})
	})
}

func TestResource_Read(t *testing.T) {
	t.Parallel()

	t.Run("no changes", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)
		config := acctest.ProviderConfig(server.URL, stringResource("test", "app:config", configValue))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			CheckDestroy:             checkKeys(server),
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					Config: config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
				},
			},
		})
	})

	t.Run("changed outside terraform", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)
		config := acctest.ProviderConfig(server.URL, stringResource("test", "app:config", configValue))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			CheckDestroy:             checkKeys(server),
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					// e.g. a client wrote it: changed back
					PreConfig: func() { setKey(t, server, "app:config", "changed by a client") },
					Config:    config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(stringAddress, plancheck.ResourceActionUpdate),
						},
					},
					Check: checkValue(server, "app:config", configValue),
				},
			},
		})
	})

	t.Run("deleted outside terraform", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)
		config := acctest.ProviderConfig(server.URL, stringResource("test", "app:config", configValue))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			CheckDestroy:             checkKeys(server),
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					// removed from the state, so it's created again
					PreConfig: func() { deleteKey(t, server, "app:config") },
					Config:    config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(stringAddress, plancheck.ResourceActionCreate),
						},
					},
					Check: checkValue(server, "app:config", configValue),
				},
			},
		})
	})

	t.Run("unmanaged keys", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)
		config := acctest.ProviderConfig(server.URL, stringResource("test", "app:config", configValue))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			// only the managed key is deleted
			CheckDestroy: checkKeys(server, "unmanaged"),
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					// left alone
					PreConfig: func() { setKey(t, server, "unmanaged", "x") },
					Config:    config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
					Check: checkKeys(server, "app:config", "unmanaged"),
				},
			},
		})
	})

	t.Run("import", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)
		config := acctest.ProviderConfig(server.URL, stringResource("test", "app:config", configValue))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			CheckDestroy:             checkKeys(server),
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					// `terraform import`, the state must match what was created
					Config:                               config,
					ResourceName:                         stringAddress,
					ImportState:                          true,
					ImportStateId:                        "app:config",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "key",
				},
			},
		})
	})

	t.Run("import block", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)
		config := acctest.ProviderConfig(server.URL, stringResource("test", "app:config", configValue))

		setKey(t, server, "app:config", configValue)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			CheckDestroy:             checkKeys(server),
			Steps: []resource.TestStep{
				{
					Config:          config,
					ResourceName:    stringAddress,
					ImportState:     true,
					ImportStateKind: resource.ImportBlockWithID,
					ImportStateId:   "app:config",
				},
			},
		})
	})

	// called directly: Read removes a missing key from the state without an error, Terraform turns that into its own
	// "Cannot import non-existent remote object" error
	t.Run("import missing key", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)

		resp := callRead(t, directResource(t, server.URL, nil), "missing")

		assert.Empty(t, resp.Diagnostics)
		assert.True(t, resp.State.Raw.IsNull())
	})

	// called directly instead of through Terraform, to compare the diagnostics exactly
	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		t.Run("key holds another type", func(t *testing.T) {
			t.Parallel()

			server := acctest.StartRedis(t)
			require.NoError(t, server.Client.HSet(context.Background(), "app:config", "field", "value").Err())

			resp := callRead(t, directResource(t, server.URL, nil), "app:config")

			acctest.AssertRedisError(t, resp.Diagnostics, "reading string")
		})
	})
}

func TestResource_Update(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			CheckDestroy:             checkKeys(server),
			Steps: []resource.TestStep{
				{
					Config: acctest.ProviderConfig(server.URL, stringResource("test", "app:config", "v1")),
				},
				{
					// new value, same key: set in place
					Config: acctest.ProviderConfig(server.URL, stringResource("test", "app:config", configValue)),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(stringAddress, plancheck.ResourceActionUpdate),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(stringAddress, "value", configValue),
						checkKeys(server, "app:config"),
						checkValue(server, "app:config", configValue),
					),
				},
			},
		})
	})

	t.Run("new key", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			CheckDestroy:             checkKeys(server),
			Steps: []resource.TestStep{
				{
					Config: acctest.ProviderConfig(server.URL, stringResource("test", "app:config", configValue)),
				},
				{
					// the old key is deleted, the new one created
					Config: acctest.ProviderConfig(server.URL, stringResource("test", "app:config:v2", configValue)),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(stringAddress, plancheck.ResourceActionDestroyBeforeCreate),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(stringAddress, "key", "app:config:v2"),
						checkKeys(server, "app:config:v2"),
						checkValue(server, "app:config:v2", configValue),
					),
				},
			},
		})
	})

	// called directly instead of through Terraform, to compare the diagnostics exactly
	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		t.Run("no permission", func(t *testing.T) {
			t.Parallel()

			server := acctest.StartRedisWithUsers(t)
			setKey(t, server, "app:config", "v1")
			env := map[string]string{"REDIS_USERNAME": "reader", "REDIS_PASSWORD": "reader-password"}

			resp := callUpdate(t, directResource(t, server.URL, env), "app:config", "v1", "v2")

			acctest.AssertRedisError(t, resp.Diagnostics, "setting string")
			// the previous value stays, in Redis and in the state
			require.NoError(t, checkValue(server, "app:config", "v1")(nil))
		})
	})
}

func TestResource_Delete(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acctest.ProviderFactories(nil),
			// keys not managed by Terraform are left alone
			CheckDestroy: checkKeys(server, "unmanaged"),
			Steps: []resource.TestStep{
				{
					PreConfig: func() { setKey(t, server, "unmanaged", "x") },
					Config: acctest.ProviderConfig(
						server.URL,
						stringResource("a", "a", "value a"),
						stringResource("b", "b", "value b"),
					),
				},
				{
					// removing one string leaves the others alone
					Config: acctest.ProviderConfig(server.URL, stringResource("a", "a", "value a")),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction("redis_string.b", plancheck.ResourceActionDestroy),
						},
					},
					Check: checkKeys(server, "a", "unmanaged"),
				},
			},
		})
	})

	// can't be reached through Terraform (refresh removes a missing key from the state first), so Delete is called
	// directly
	t.Run("already deleted", func(t *testing.T) {
		t.Parallel()

		server := acctest.StartRedis(t)

		resp := callDelete(t, directResource(t, server.URL, nil), "missing", "x")

		assert.Empty(t, resp.Diagnostics)
	})

	// called directly instead of through Terraform, to compare the diagnostics exactly
	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		t.Run("no permission", func(t *testing.T) {
			t.Parallel()

			server := acctest.StartRedisWithUsers(t)
			setKey(t, server, "app:config", "v1")
			env := map[string]string{"REDIS_USERNAME": "reader", "REDIS_PASSWORD": "reader-password"}

			resp := callDelete(t, directResource(t, server.URL, env), "app:config", "v1")

			acctest.AssertRedisError(t, resp.Diagnostics, "deleting string")
			require.NoError(t, checkKeys(server, "app:config")(nil))
		})
	})
}
