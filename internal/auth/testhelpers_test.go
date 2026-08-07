package auth

import "context"

type fakeUserRepository struct {
	createCalls int
	findCalls   int
	createFunc  func(context.Context, string, string) (User, error)
	findFunc    func(context.Context, string) (User, error)
}

func (f *fakeUserRepository) Create(ctx context.Context, login, passwordHash string) (User, error) {
	f.createCalls++
	if f.createFunc == nil {
		return User{}, nil
	}

	return f.createFunc(ctx, login, passwordHash)
}

func (f *fakeUserRepository) FindByLogin(ctx context.Context, login string) (User, error) {
	f.findCalls++
	if f.findFunc == nil {
		return User{}, ErrUserNotFound
	}

	return f.findFunc(ctx, login)
}

type fakePasswords struct {
	hashCalls   int
	verifyCalls int
	hashFunc    func(string) (string, error)
	verifyFunc  func(string, string) error
}

func (f *fakePasswords) Hash(password string) (string, error) {
	f.hashCalls++
	if f.hashFunc == nil {
		return "hash", nil
	}

	return f.hashFunc(password)
}

func (f *fakePasswords) Verify(passwordHash, password string) error {
	f.verifyCalls++
	if f.verifyFunc == nil {
		return nil
	}

	return f.verifyFunc(passwordHash, password)
}

type fakeTokenIssuer struct {
	calls     int
	issueFunc func(int64) (IssuedToken, error)
}

func (f *fakeTokenIssuer) Issue(userID int64) (IssuedToken, error) {
	f.calls++
	if f.issueFunc == nil {
		return IssuedToken{Value: "token"}, nil
	}

	return f.issueFunc(userID)
}
