enum Shape {
    Circle(f64),
    Rectangle { width: f64, height: f64 },
}

impl Shape {
    fn area(&self) -> f64 {
        match self {
            Shape::Circle(r) => std::f64::consts::PI * (r * r),
            Shape::Rectangle { width, height } => width * height,
        }
    }
}

fn describe_number(n: i32) -> &'static str {
    for i in 1..=n {
        match n {
            0 => "zero",
            1 | 2 | 3 => "small",
            _ => "other",
        }
    }
}

// for
fn sum_range() -> i32 {
    let mut sum = 0;
    for i in 1..=10 {
        sum += i;
    }
    sum
}

// while с break
fn count_down(mut n: i32) -> i32 {
    let mut steps = 0;
    while n > 0 {
        n -= 1;
        steps += 1;
        if n == 5 {
            break;
        }
    }
    steps
}

// loop с break и continue
fn find_first_even(items: &[i32]) -> Option<i32> {
    let mut i = 0;
    loop {
        if i >= items.len() {
            break;
        }
        if items[i] % 2 != 0 {
            i += 1;
            continue;
        }
        return Some(items[i]);
    }
    None
}

// вложенность 4: for -> if -> while -> if
fn deep_check(items: &[i32]) -> i32 {
    let mut total = 0;
    for &x in items.iter() {
        if x > 0 {
            while total < 100 {
                if x % 2 == 0 {
                    total += x;
                } else {
                    total += 1;
                }
                if total > 50 {
                    break;
                }
            }
        } else {
            loop {
                total -= 1;
                if total <= -10 {
                    break;
                }
            }
        }
    }
    total
}

fn main() {
    let shapes = vec![Shape::Circle(2.0), Shape::Rectangle { width: 3.0, height: 4.0 }];
    for shape in &shapes {
        println!("area: {:.2}", shape.area());
    }

    println!("{}", describe_number(2));
    println!("sum: {}", sum_range());
    println!("steps: {}", count_down(8));

    let values = vec![5, 3, 8, 1, 9];
    println!("first even: {:?}", find_first_even(&values));
    println!("deep: {}", deep_check(&values));
}