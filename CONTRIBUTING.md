# Contributing to Shadow Mock

Thank you for helping. This page covers how to propose a change and the two things every
contribution must include: a sign-off, and the licence grant below.

## Making a change

* Work on a feature branch and open a pull request against `main`. One pull request per
  self-contained change.
* Read [docs/CODING_STANDARDS.md](docs/CODING_STANDARDS.md) first. Run `make lint` and
  `make test` before you push. Behaviour changes come with acceptance specs in
  [features/](features/).
* A new dependency needs a one-line justification in the pull request and an acceptable licence
  (MIT, BSD, Apache-2.0 or ISC).

## Sign your commits (DCO)

Every commit must carry a `Signed-off-by` line. It certifies the
[Developer Certificate of Origin](DCO): that you wrote the change, or otherwise have the right
to submit it under this project's licence.

```bash
git commit -s -m "Add the thing"
```

That appends `Signed-off-by: Your Name <you@example.com>`, using your git `user.name` and
`user.email`. The name must be your real name. To fix commits you already made, run
`git rebase --signoff main` and force-push your branch. A check on every pull request fails if a
commit lacks the line (merge commits are exempt).

## Licence grant to the project owner

Shadow Mock is released under the [GNU Affero General Public License v3.0](LICENSE) and is also
offered commercially, including as a hosted service. That only works if the project owner may
license contributions on other terms, so by signing off a commit you also agree to the
following.

1. **You keep your copyright.** You are not transferring ownership of your contribution.
2. **Grant.** You grant the Project Owner a perpetual, worldwide, non-exclusive, royalty-free,
   irrevocable licence to use, reproduce, modify, publicly display and perform, distribute and
   sublicense your contribution, and works based on it, under any terms, including proprietary
   terms. The "Project Owner" is the maintainer of the `pwgith/shadow-mock` repository and
   anyone who takes over the project from them.
3. **Patents.** You grant the Project Owner and everyone who receives the software a perpetual,
   worldwide, royalty-free licence under any patents you hold that your contribution
   necessarily infringes, for the contribution alone or combined with the project.
4. **Open source stays open.** Versions of Shadow Mock the Project Owner has already released
   under the AGPL stay available under the AGPL. The grant above covers what the owner does
   with later versions.
5. **Your right to contribute.** You confirm that you have the right to make this grant. If
   your employer owns what you write, you have their permission to contribute.

If you cannot agree to this grant, please say so in the pull request before you contribute, so
the maintainer can decide whether to accept the change on other terms.

## Reporting a security problem

Please do not open a public issue. Contact the maintainer privately through the email address on
the maintainer's GitHub profile.
