/*
Copyright 2017 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package trace

import (
  "golang.org/x/net/context"
)

type fakeSpanFactory struct{}

func (fakeSpanFactory) New(Span, string, SpanType) Span                           { return fakeSpan{} }
func (fakeSpanFactory) FromContext(context.Context) (Span, bool)                  { return nil, false }
func (fakeSpanFactory) NewContext(parent context.Context, _ Span) context.Context { return parent }

// fakeSpan implements Span with no-op methods.
type fakeSpan struct{}

func (fakeSpan) Finish()                      {}
func (fakeSpan) Annotate(string, interface{}) {}

