package accrual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/order"
)

// orderPath принимает номер отдельным параметром пути, а не склейкой строк.
const orderPath = "/api/orders/{number}"

// requestTimeout ограничивает один запрос к системе расчёта.
const requestTimeout = 5 * time.Second

// maxResponseBytes держит тело чужого ответа в разумных пределах.
const maxResponseBytes = 64 << 10

// OrderInfo несёт результат расчёта в терминах домена заказов.
type OrderInfo struct {
	Number  string
	Status  order.Status
	Accrual *money.Points
}

// orderResponse повторяет тело ответа системы расчёта.
type orderResponse struct {
	Order   string        `json:"order"`
	Status  Status        `json:"status"`
	Accrual *money.Points `json:"accrual"`
}

// Client запрашивает расчёт начислений во внешней системе.
type Client struct {
	http *resty.Client
}

// New отвергает непригодный адрес и дописывает схему, если её нет.
func New(address string) (*Client, error) {
	baseURL, err := normalizeAddress(address)
	if err != nil {
		return nil, err
	}

	client := resty.New().
		SetBaseURL(baseURL).
		SetTimeout(requestTimeout).
		SetResponseBodyLimit(maxResponseBytes).
		SetCookieJar(nil).
		SetRedirectPolicy(resty.NoRedirectPolicy())

	return &Client{http: client}, nil
}

// OrderInfo возвращает расчёт по номеру заказа или типизированный исход.
// Номер заказа несёт любая ошибка вызова, включая ожидаемые исходы.
func (c *Client) OrderInfo(ctx context.Context, number string) (OrderInfo, error) {
	info, err := c.orderInfo(ctx, number)
	if err != nil {
		return OrderInfo{}, fmt.Errorf("accrual: order %s: %w", number, err)
	}

	return info, nil
}

func (c *Client) orderInfo(ctx context.Context, number string) (OrderInfo, error) {
	response, err := c.http.R().
		SetContext(ctx).
		SetPathParam("number", number).
		Get(orderPath)
	if err != nil {
		return OrderInfo{}, requestError(err)
	}

	switch response.StatusCode() {
	case http.StatusOK:
		return parseOrderInfo(number, response.Body())
	case http.StatusNoContent:
		return OrderInfo{}, ErrNotRegistered
	case http.StatusTooManyRequests:
		return OrderInfo{}, &TooManyRequestsError{RetryAfter: retryAfter(response.Header())}
	default:
		return OrderInfo{}, &UnexpectedStatusError{StatusCode: response.StatusCode()}
	}
}

// requestError прячет сентинелы HTTP-библиотеки за ошибками пакета.
func requestError(err error) error {
	if errors.Is(err, resty.ErrResponseBodyTooLarge) {
		return ErrResponseTooLarge
	}

	return err
}

func parseOrderInfo(number string, body []byte) (OrderInfo, error) {
	var response orderResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return OrderInfo{}, fmt.Errorf("%w: decode: %w", ErrMalformedResponse, err)
	}
	if err := response.validate(number); err != nil {
		return OrderInfo{}, err
	}

	status, err := response.Status.ToOrderStatus()
	if err != nil {
		return OrderInfo{}, err
	}

	info := OrderInfo{Number: response.Order, Status: status}
	// сумма имеет смысл только вместе с завершённым расчётом
	if status == order.StatusProcessed {
		info.Accrual = response.Accrual
	}

	return info, nil
}

// validate ловит ответ о чужом заказе и невозможную сумму начисления.
func (r orderResponse) validate(number string) error {
	if r.Order != number {
		return fmt.Errorf(
			"%w: response carries order %q instead of %q",
			ErrMalformedResponse,
			r.Order,
			number,
		)
	}
	if r.Accrual != nil && r.Accrual.IsNegative() {
		return fmt.Errorf(
			"%w: accrual %v must not be negative",
			ErrMalformedResponse,
			r.Accrual.Float64(),
		)
	}

	return nil
}
