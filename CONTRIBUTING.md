# Contributing to audiobookshelf-hardcover-sync

Thank you for your interest in contributing! Here are some guidelines to help you get started:

## How to Contribute
- Fork the repository and create your branch from `develop`.
- Make your changes with clear commit messages.
- Ensure your code passes all tests and builds successfully.
- Open a pull request targeting `develop` with a clear description of your changes.

### Branching Model
- **develop**: Integration branch for new features and changes.
- **main**: Always stable. Releases are cut from `main`.
- **feature/***: Create from `develop`, merge back into `develop` via PR.
- **hotfix/***: Create from `main` for urgent fixes, merge back into `main` and `develop`.

## Code Style
- Follow idiomatic Go conventions.
- Keep code and Docker images minimal and secure.

## Issues
- Use GitHub Issues to report bugs or request features.
- Please provide as much detail as possible.

## Questions
For questions, open an issue or contact the maintainer at patrice@brendamour.net.

## License
By contributing, you agree that your contributions will be licensed under the MIT License.
