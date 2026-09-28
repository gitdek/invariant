# Set up the factory's GitHub App

The factory acts as its own bot once it has a GitHub App ([D-0041](../decisions/log.md)). It comments, pushes and merges as the App's bot, and only people's comments can direct it or ratify.

For gitdek/invariant, the App is **Invariant Code Factory**: App ID `5079269`, bot `invariant-code-factory[bot]`, installed on this repository only ([D-0044](../decisions/log.md)). `Invariant Factory` was taken, because it's reserved for an existing account. Creating the App and holding its private key is the repository owner's job. The factory only ever reads the key from the path you give it.

## Create the App

1. Open [github.com/settings/apps/new](https://github.com/settings/apps/new).
2. **GitHub App name:** a name nobody has taken, such as `Invariant Code Factory`. Names are unique across GitHub, and a name that matches an existing account is reserved for it.
3. **Homepage URL:** `https://puglisij.com`
4. Leave **Callback URL** and **Setup URL** empty, and leave **Request user authorization (OAuth) during installation** unticked.
5. **Webhook:** untick **Active**. The factory polls, so it needs no webhook.
6. **Repository permissions:**

   | Permission | Access | Why |
   | :-- | :-- | :-- |
   | Actions | Read-only | Tell a gate job that ran from one GitHub never started, as when the account's Actions minutes run out (D-0081). Without it, the factory says it can't tell (D-0095) |
   | Checks | Read-only | Read `invariant/gate`'s result before merging |
   | Contents | Read and write | Push the issue's branch, merge, delete the branch |
   | Issues | Read and write | Post on issues, set labels |
   | Metadata | Read-only | Set automatically |
   | Pull requests | Read and write | Open and merge pull requests |

   Leave everything else at **No access**, especially **Workflows**. Without it, the App's token can't change anything in `.github/`.
7. **Where can this GitHub App be installed?** Only on this account.
8. Click **Create GitHub App**.

## Keep its key

1. On the App's page, note the **App ID**, a number near the top. It isn't a secret.
2. Under **Private keys**, click **Generate a private key**. Your browser downloads a `.pem` file.
3. Move the key out of Downloads and make it readable only by you:

   ```bash
   mkdir -p ~/.config/invariant && mv ~/Downloads/invariant-code-factory*.private-key.pem ~/.config/invariant/factory.pem && chmod 600 ~/.config/invariant/factory.pem
   ```

   The file is named after the App's slug. Never commit the key or paste it anywhere. The App doesn't need a client secret, which is only for signing users in.

## Install it

1. On the App's page, click **Install App** in the sidebar, then **Install** next to your account.
2. Choose **Only select repositories**, pick `gitdek/invariant`, and click **Install**.

## Run the factory as the App

```bash
go run ./cmd/invariant watch -repo gitdek/invariant -app-id 5079269
```

`-app-key` defaults to `~/.config/invariant/factory.pem`. The watcher reads the repository with your own git credentials. Everything it writes, it writes as the App.
