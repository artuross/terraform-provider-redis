//go:build integration

package functionlibrary_test

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/artuross/terraform-provider-redis/internal/provider/functionlibrary"
)

const libraryAddress = "redis_function_library.test"

func TestResource_Create(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					Config: providerConfig(server.url, libraryResource("test", luaLibrary("lib", "fn", "v1"))),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(libraryAddress, plancheck.ResourceActionCreate),
							// name is known while planning, it comes from the shebang
							plancheck.ExpectKnownValue(
								libraryAddress,
								tfjsonpath.New("name"),
								knownvalue.StringExact("lib"),
							),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(libraryAddress, "name", "lib"),
						resource.TestCheckResourceAttr(libraryAddress, "content", luaLibrary("lib", "fn", "v1")),
						server.checkLibraries("lib"),
						server.checkFCall("fn", "v1"),
					),
				},
			},
		})
	})

	t.Run("multiple libraries", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					Config: providerConfig(
						server.url,
						libraryResource("a", luaLibrary("a", "fn_a", "a")),
						libraryResource("b", luaLibrary("b", "fn_b", "b")),
					),
					Check: resource.ComposeAggregateTestCheckFunc(
						server.checkLibraries("a", "b"),
						server.checkFCall("fn_a", "a"),
						server.checkFCall("fn_b", "b"),
					),
				},
			},
		})
	})

	t.Run("with credentials", func(t *testing.T) {
		t.Parallel()

		server := startRedisWithUsers(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(map[string]string{
				"REDIS_USERNAME": "admin",
				"REDIS_PASSWORD": "admin-password",
			}),
			CheckDestroy: server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					Config: providerConfig(server.url, libraryResource("test", luaLibrary("lib", "fn", "v1"))),
					Check: resource.ComposeAggregateTestCheckFunc(
						server.checkLibraries("lib"),
						server.checkFCall("fn", "v1"),
					),
				},
			},
		})
	})

	t.Run("with tls", func(t *testing.T) {
		t.Parallel()

		server := startRedisWithTLS(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					// the certificate is self-signed
					Config: providerConfig(
						server.url+"?skip_verify=true",
						libraryResource("test", luaLibrary("lib", "fn", "v1")),
					),
					Check: resource.ComposeAggregateTestCheckFunc(
						server.checkLibraries("lib"),
						server.checkFCall("fn", "v1"),
					),
				},
			},
		})
	})

	// called directly instead of through Terraform, to compare the diagnostics exactly
	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		type testCase struct {
			name string
			// starts the server, startRedis when nil
			start func(t *testing.T) *redisServer
			// provider url, the server's url when nil
			url func(server *redisServer) string
			// provider environment, e.g. credentials
			env map[string]string
			// runs before creating, e.g. to load conflicting libraries
			setup         func(t *testing.T, server *redisServer)
			content       string
			wantLibraries []string
		}

		tests := []testCase{
			{
				name:    "unknown engine",
				content: "#!python name=lib\n",
			},
			{
				name:    "syntax error",
				content: "#!lua name=lib\nredis.register_function('fn', function() return end end)\n",
			},
			{
				name:    "error while loading",
				content: "#!lua name=lib\nlocal boom = nil\nboom()\n",
			},
			{
				name:    "no functions",
				content: "#!lua name=lib\n",
			},
			{
				name: "invalid flag",
				content: "#!lua name=lib\n" +
					"redis.register_function{function_name='fn', callback=function() return 1 end, flags={'nope'}}\n",
			},
			{
				name: "library already exists",
				setup: func(t *testing.T, server *redisServer) {
					server.loadLibrary(t, luaLibrary("lib", "fn", "existing"), false)
				},
				content:       luaLibrary("lib", "fn", "new"),
				wantLibraries: []string{"lib"},
			},
			{
				name: "function exists in another library",
				setup: func(t *testing.T, server *redisServer) {
					server.loadLibrary(t, luaLibrary("other", "fn", "existing"), false)
				},
				content:       luaLibrary("lib", "fn", "new"),
				wantLibraries: []string{"other"},
			},
			{
				name:    "no credentials",
				start:   startRedisWithUsers,
				content: luaLibrary("lib", "fn", "v1"),
			},
			{
				name:    "wrong password",
				start:   startRedisWithUsers,
				env:     map[string]string{"REDIS_USERNAME": "admin", "REDIS_PASSWORD": "wrong"},
				content: luaLibrary("lib", "fn", "v1"),
			},
			{
				name:    "unknown user",
				start:   startRedisWithUsers,
				env:     map[string]string{"REDIS_USERNAME": "nobody", "REDIS_PASSWORD": "admin-password"},
				content: luaLibrary("lib", "fn", "v1"),
			},
			{
				name:    "no permission",
				start:   startRedisWithUsers,
				env:     map[string]string{"REDIS_USERNAME": "reader", "REDIS_PASSWORD": "reader-password"},
				content: luaLibrary("lib", "fn", "v1"),
			},
			{
				name:    "tls with untrusted certificate",
				start:   startRedisWithTLS,
				content: luaLibrary("lib", "fn", "v1"),
			},
			{
				name:  "no tls to a tls server",
				start: startRedisWithTLS,
				url: func(server *redisServer) string {
					return "redis://" + strings.TrimPrefix(server.url, "rediss://")
				},
				content: luaLibrary("lib", "fn", "v1"),
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				start := tt.start
				if start == nil {
					start = func(t *testing.T) *redisServer { return startRedis(t) }
				}

				server := start(t)

				if tt.setup != nil {
					tt.setup(t, server)
				}

				url := server.url
				if tt.url != nil {
					url = tt.url(server)
				}

				resp := callCreate(t, directResource(t, url, tt.env), tt.content)

				assertRedisError(t, resp.Diagnostics, "loading function library")
				assert.True(t, resp.State.Raw.IsNull())

				// nothing was loaded, existing libraries are untouched
				require.NoError(t, server.checkLibraries(tt.wantLibraries...)(nil))
			})
		}

		// functions were added in Redis 7.0
		t.Run("unsupported server", func(t *testing.T) {
			t.Parallel()

			server := startRedisImage(t, "redis:6.2.24-alpine3.21")
			content := luaLibrary("lib", "fn", "v1")

			resp := callCreate(t, directResource(t, server.url, nil), content)

			assertRedisError(t, resp.Diagnostics, "loading function library")

			// our explanation exactly, followed by the server's error
			explanation, _, found := strings.Cut(resp.Diagnostics[0].Detail(), "\n\nServer error: ")
			require.True(t, found)
			assert.Equal(
				t,
				"Redis 6.2.24 doesn't support functions, they require Redis 7.0 or later, or Valkey 7.2 or later.",
				explanation,
			)
		})

		t.Run("unreachable", func(t *testing.T) {
			t.Parallel()

			// nothing listens on port 1
			resp := callCreate(t, directResource(t, "redis://127.0.0.1:1", nil), luaLibrary("lib", "fn", "v1"))

			assertRedisError(t, resp.Diagnostics, "loading function library")
		})
	})
}

func TestResource_Read(t *testing.T) {
	t.Parallel()

	content := luaLibrary("lib", "fn", "v1")

	t.Run("no changes", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)
		config := providerConfig(server.url, libraryResource("test", content))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
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

	t.Run("deleted outside terraform", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)
		config := providerConfig(server.url, libraryResource("test", content))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					// removed from the state, so it's created again
					PreConfig: func() { server.deleteLibrary(t, "lib") },
					Config:    config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(libraryAddress, plancheck.ResourceActionCreate),
						},
					},
					Check: server.checkFCall("fn", "v1"),
				},
			},
		})
	})

	t.Run("changed outside terraform", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)
		config := providerConfig(server.url, libraryResource("test", content))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					// the code in Redis is read back, so it's changed back
					PreConfig: func() { server.loadLibrary(t, luaLibrary("lib", "fn", "changed"), true) },
					Config:    config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(libraryAddress, plancheck.ResourceActionUpdate),
						},
					},
					Check: server.checkFCall("fn", "v1"),
				},
			},
		})
	})

	t.Run("unmanaged libraries", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)
		config := providerConfig(server.url, libraryResource("test", content))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			// only the managed library is deleted
			CheckDestroy: server.checkLibraries("unmanaged"),
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					// left alone
					PreConfig: func() { server.loadLibrary(t, luaLibrary("unmanaged", "unmanaged_fn", "x"), false) },
					Config:    config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
					Check: server.checkLibraries("lib", "unmanaged"),
				},
			},
		})
	})

	t.Run("similarly named library", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)
		config := providerConfig(server.url, libraryResource("test", content))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			// only the managed library is deleted
			CheckDestroy: server.checkLibraries("lib_other"),
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					// a library whose name starts with the managed one's must not be mistaken for it
					PreConfig: func() {
						server.deleteLibrary(t, "lib")
						server.loadLibrary(t, luaLibrary("lib_other", "other_fn", "x"), false)
					},
					Config: config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(libraryAddress, plancheck.ResourceActionCreate),
						},
					},
					Check: server.checkLibraries("lib", "lib_other"),
				},
			},
		})
	})

	t.Run("import", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)
		config := providerConfig(server.url, libraryResource("test", content))

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					Config: config,
				},
				{
					// `terraform import`, the state must match what was created
					Config:                               config,
					ResourceName:                         libraryAddress,
					ImportState:                          true,
					ImportStateId:                        "lib",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
			},
		})
	})

	t.Run("import block", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)
		config := providerConfig(server.url, libraryResource("test", content))

		server.loadLibrary(t, content, false)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					Config:          config,
					ResourceName:    libraryAddress,
					ImportState:     true,
					ImportStateKind: resource.ImportBlockWithID,
					ImportStateId:   "lib",
				},
			},
		})
	})

	// called directly: Read removes a missing library from the state without an error, Terraform turns that into its
	// own "Cannot import non-existent remote object" error
	t.Run("import missing library", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)

		resp := callRead(t, directResource(t, server.url, nil), "missing")

		assert.Empty(t, resp.Diagnostics)
		assert.True(t, resp.State.Raw.IsNull())
	})
}

func TestResource_Update(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					Config: providerConfig(server.url, libraryResource("test", luaLibrary("lib", "fn", "v1"))),
				},
				{
					// new code, same name: replaced in place
					Config: providerConfig(server.url, libraryResource("test", luaLibrary("lib", "fn", "v2"))),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(libraryAddress, plancheck.ResourceActionUpdate),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(libraryAddress, "name", "lib"),
						resource.TestCheckResourceAttr(libraryAddress, "content", luaLibrary("lib", "fn", "v2")),
						server.checkLibraries("lib"),
						server.checkFCall("fn", "v2"),
					),
				},
			},
		})
	})

	t.Run("removed functions", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					Config: providerConfig(server.url, libraryResource("test", luaLibrary("lib", "fn", "v1"))),
				},
				{
					// functions removed from the code are removed from Redis too
					Config: providerConfig(
						server.url,
						libraryResource("test", luaLibrary("lib", "renamed_fn", "v1")),
					),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(libraryAddress, plancheck.ResourceActionUpdate),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						server.checkFCall("renamed_fn", "v1"),
						checkError(server.checkFCall("fn", "v1"), "FCALL fn: ERR Function not found"),
					),
				},
			},
		})
	})

	t.Run("new library name", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			CheckDestroy:             server.checkLibraries(),
			Steps: []resource.TestStep{
				{
					Config: providerConfig(server.url, libraryResource("test", luaLibrary("lib", "fn", "v1"))),
				},
				{
					// the old library is deleted before the new one is loaded
					Config: providerConfig(
						server.url,
						libraryResource("test", luaLibrary("renamed", "fn", "v2")),
					),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(
								libraryAddress,
								plancheck.ResourceActionDestroyBeforeCreate,
							),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(libraryAddress, "name", "renamed"),
						server.checkLibraries("renamed"),
						server.checkFCall("fn", "v2"),
					),
				},
			},
		})
	})

	// called directly instead of through Terraform, to compare the diagnostics exactly
	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		type testCase struct {
			name    string
			content string
		}

		tests := []testCase{
			{
				name:    "syntax error",
				content: "#!lua name=lib\nredis.register_function('fn', function() return end end)\n",
			},
			{
				name:    "function exists in another library",
				content: luaLibrary("lib", "taken", "v2"),
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				server := startRedis(t)
				content := luaLibrary("lib", "fn", "v1")

				server.loadLibrary(t, content, false)
				server.loadLibrary(t, luaLibrary("other", "taken", "x"), false)

				resp := callUpdate(t, directResource(t, server.url, nil), "lib", content, tt.content)

				assertRedisError(t, resp.Diagnostics, "replacing function library")

				// the previous version stays loaded and in the state
				s := schemaOf(t, functionlibrary.NewResource())
				assert.Equal(t, libraryObject(t, s, content, "lib"), resp.State.Raw)
				require.NoError(t, server.checkFCall("fn", "v1")(nil))
				require.NoError(t, server.checkLibraries("lib", "other")(nil))
			})
		}
	})
}

func TestResource_Delete(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)

		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: providerFactories(nil),
			// libraries not managed by Terraform are left alone
			CheckDestroy: server.checkLibraries("unmanaged"),
			Steps: []resource.TestStep{
				{
					PreConfig: func() { server.loadLibrary(t, luaLibrary("unmanaged", "unmanaged_fn", "x"), false) },
					Config: providerConfig(
						server.url,
						libraryResource("a", luaLibrary("a", "fn_a", "a")),
						libraryResource("b", luaLibrary("b", "fn_b", "b")),
					),
				},
				{
					// removing one library leaves the others alone
					Config: providerConfig(server.url, libraryResource("a", luaLibrary("a", "fn_a", "a"))),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction("redis_function_library.b", plancheck.ResourceActionDestroy),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						server.checkLibraries("a", "unmanaged"),
						server.checkFCall("fn_a", "a"),
						checkError(server.checkFCall("fn_b", "b"), "FCALL fn_b: ERR Function not found"),
					),
				},
			},
		})
	})

	// can't be reached through Terraform (refresh removes a missing library from the state first), so Delete is
	// called directly
	t.Run("already deleted", func(t *testing.T) {
		t.Parallel()

		server := startRedis(t)

		resp := callDelete(t, directResource(t, server.url, nil), "missing", luaLibrary("missing", "fn", "x"))

		assert.Empty(t, resp.Diagnostics)
	})
}
