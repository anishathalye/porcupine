# Porcupine

Porcupine is a fast linearizability checker written in Go. Porcupine implements
the algorithm described in [Faster linearizability checking via
P-compositionality][faster-linearizability-checking], an optimization of the
algorithm described in [Testing for Linearizability][linearizability-testing].

Porcupine is faster and can handle more histories than [Knossos][knossos]'s
linearizability checker.

## Usage

Porcupine takes an executable model of a system along with a history, and it
runs a decision procedure to determine if the history is linearizable with
respect to the model.

Porcupine supports specifying history in two ways, either as a list of
operations with given call and return times, or as a list of call/return events
in time order.

See [`model.go`](model.go) for documentation on how to write a model or specify
histories.

Once you've written a model and have a history, you can use the
`CheckOperations` and `CheckEvents` functions to determine if your history is
linearizable.

See [`porcupine_test.go`](porcupine-test.go) for examples on how to write
models and histories.

## Citation

If you use Porcupine in any way in academic work, please cite the following:

```
@misc{athalye2017porcupine,
  author = {Anish Athalye},
  title = {Porcupine: A fast linearizability checker in {Go}},
  year = {2017},
  howpublished = {\url{https://github.com/anishathalye/porcupine}},
  note = {commit xxxxxxx}
}
```

## License

Copyright (c) 2017 Anish Athalye. Released under the MIT License. See
[LICENSE.md][license] for details.

[faster-linearizability-checking]: https://arxiv.org/pdf/1504.00204.pdf
[linearizability-testing]: http://www.cs.ox.ac.uk/people/gavin.lowe/LinearizabiltyTesting/paper.pdf
[knossos]: https://github.com/jepsen-io/knossos
[license]: LICENSE.md
